package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// simRequest es lo que manda la página al pulsar "Iniciar simulación".
type simRequest struct {
	Mode        string `json:"mode"` // "batch" (una tanda) o "continuous" (durante un tiempo)
	Combat      int    `json:"combat"`
	Chase       int    `json:"chase"`
	Racing      int    `json:"racing"`
	Special     bool   `json:"special"`
	DelayMs     int    `json:"delayMs"`
	Seed        uint64 `json:"seed"`
	DurationSec int    `json:"durationSec"`
	Parallel    int    `json:"parallel"`
}

// args valida la petición y la traduce a los flags del simulador.
// La página ya limita los valores, pero el servidor no confía en ella.
func (q simRequest) args(ingestURL string) ([]string, error) {
	inRange := func(name string, v, lo, hi int) error {
		if v < lo || v > hi {
			return fmt.Errorf("%s debe estar entre %d y %d", name, lo, hi)
		}
		return nil
	}
	if err := inRange("la pausa entre eventos (ms)", q.DelayMs, 0, 2000); err != nil {
		return nil, err
	}

	args := []string{
		"-url", ingestURL,
		"-delay", fmt.Sprintf("%dms", q.DelayMs),
		"-special=" + strconv.FormatBool(q.Special),
	}
	if q.Seed != 0 {
		args = append(args, "-seed", strconv.FormatUint(q.Seed, 10))
	}

	switch q.Mode {
	case "batch":
		for _, c := range []struct {
			name string
			v    int
		}{{"combat", q.Combat}, {"chase", q.Chase}, {"racing", q.Racing}} {
			if err := inRange("las partidas de "+c.name, c.v, 0, 50); err != nil {
				return nil, err
			}
		}
		if q.Combat+q.Chase+q.Racing == 0 && !q.Special {
			return nil, errors.New("elige al menos una partida o incluye los casos especiales")
		}
		args = append(args,
			"-combat", strconv.Itoa(q.Combat),
			"-chase", strconv.Itoa(q.Chase),
			"-racing", strconv.Itoa(q.Racing))
	case "continuous":
		if err := inRange("la duración (s)", q.DurationSec, 10, 3600); err != nil {
			return nil, err
		}
		if err := inRange("las partidas a la vez", q.Parallel, 1, 50); err != nil {
			return nil, err
		}
		args = append(args,
			"-duration", fmt.Sprintf("%ds", q.DurationSec),
			"-parallel", strconv.Itoa(q.Parallel))
	default:
		return nil, fmt.Errorf("modo desconocido %q: usa batch o continuous", q.Mode)
	}
	return args, nil
}

// logLine es una línea de la salida del simulador.
type logLine struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// simState es lo que la página muestra sobre la simulación.
type simState struct {
	Run        int        `json:"run"`    // número de ejecución: cambia en cada inicio
	Status     string     `json:"status"` // idle, building, running, stopping, finished, stopped, failed
	Mode       string     `json:"mode,omitempty"`
	Args       []string   `json:"args,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func (s simState) active() bool {
	return s.Status == "building" || s.Status == "running" || s.Status == "stopping"
}

const maxLines = 400

var errBusy = errors.New("ya hay una simulación corriendo")

// simRunner lanza el simulador como un proceso aparte y guarda su salida.
// Solo corre una simulación a la vez. Todo su estado está protegido por mu,
// porque lo leen las peticiones HTTP mientras la goroutine del proceso lo cambia.
type simRunner struct {
	ingestURL string

	mu      sync.Mutex
	bin     string // ruta del simulador; vacío = buscarlo o compilarlo la primera vez
	state   simState
	cmd     *exec.Cmd
	done    chan struct{} // se cierra cuando termina la ejecución actual
	stopReq bool
	lines   []logLine
	seq     int64
}

func newSimRunner(ingestURL, bin string) *simRunner {
	return &simRunner{ingestURL: ingestURL, bin: bin, state: simState{Status: "idle"}}
}

// Start valida la petición y lanza el simulador en segundo plano.
func (r *simRunner) Start(q simRequest) error {
	args, err := q.args(r.ingestURL)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.active() {
		return errBusy
	}
	now := time.Now().UTC()
	r.state = simState{Run: r.state.Run + 1, Status: "building", Mode: q.Mode, Args: args, StartedAt: &now}
	r.lines = nil // la consola de la página se limpia al ver un Run nuevo
	r.stopReq = false
	r.cmd = nil
	r.done = make(chan struct{})

	go r.run(args, r.done)
	return nil
}

// run es la goroutine de una ejecución: encuentra el binario, lo arranca,
// copia su salida línea por línea y anota cómo terminó.
func (r *simRunner) run(args []string, done chan struct{}) {
	defer close(done)

	bin, err := r.resolve()
	if err != nil {
		r.finish(-1, err.Error())
		return
	}

	// stdout y stderr van al mismo tubo: la salida se ve en el orden en que salió.
	pr, pw := io.Pipe()
	cmd := exec.Command(bin, args...)
	cmd.Stdout = pw
	cmd.Stderr = pw

	r.mu.Lock()
	if r.stopReq { // pidieron detener mientras se compilaba
		r.mu.Unlock()
		r.finish(-1, "")
		return
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		r.finish(-1, "no se pudo iniciar el simulador: "+err.Error())
		return
	}
	r.cmd = cmd
	r.state.Status = "running"
	r.mu.Unlock()
	r.addLine("$ simulator " + strings.Join(args, " "))

	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			r.addLine(sc.Text())
		}
	}()

	err = cmd.Wait()
	_ = pw.Close() // fin de la salida: el lector termina su ciclo
	<-scanned

	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode() // -1 si lo terminó una señal (por ejemplo, al detenerlo)
	} else if err != nil {
		code = -1
	}
	r.finish(code, "")
}

func (r *simRunner) finish(code int, errText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.state.FinishedAt = &now
	r.state.ExitCode = &code
	r.state.Error = errText
	switch {
	case r.stopReq:
		r.state.Status = "stopped"
	case code == 0 && errText == "":
		r.state.Status = "finished"
	default:
		r.state.Status = "failed"
	}
	r.cmd = nil
	if errText != "" {
		r.appendLocked(errText)
	}
}

// Stop pide al simulador que termine. Primero con Ctrl+C (SIGINT): en modo
// continuo el simulador termina las partidas en curso y se despide ordenado.
// Si en 20 segundos no terminó, se le obliga.
func (r *simRunner) Stop() {
	r.mu.Lock()
	if !r.state.active() {
		r.mu.Unlock()
		return
	}
	r.stopReq = true
	r.state.Status = "stopping"
	cmd, done := r.cmd, r.done
	r.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return // todavía compilando: run() ve stopReq y no arranca el proceso
	}
	r.addLine("deteniendo la simulación...")
	if err := cmd.Process.Signal(os.Interrupt); err != nil { // en Windows no existe SIGINT
		_ = cmd.Process.Kill()
		return
	}
	go func() {
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
}

// StopAndWait detiene la simulación y espera a que termine (para el apagado del panel).
func (r *simRunner) StopAndWait(timeout time.Duration) {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	r.Stop()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// Snapshot devuelve el estado y las líneas con número mayor que since.
func (r *simRunner) Snapshot(since int64) (simState, []logLine, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]logLine, 0)
	for _, l := range r.lines {
		if l.Seq > since {
			out = append(out, l)
		}
	}
	return r.state, out, r.seq
}

// logPrefix es la fecha que el paquete log de Go pone al inicio de cada línea.
// La página muestra su propia hora, así que se quita.
var logPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

func (r *simRunner) addLine(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendLocked(text)
}

func (r *simRunner) appendLocked(text string) {
	r.seq++
	r.lines = append(r.lines, logLine{Seq: r.seq, At: time.Now().UTC(), Text: logPrefix.ReplaceAllString(text, "")})
	if len(r.lines) > maxLines {
		r.lines = append([]logLine(nil), r.lines[len(r.lines)-maxLines:]...)
	}
}

// resolve encuentra el binario del simulador, en este orden:
//  1. SIMULATOR_BIN, si está definida.
//  2. Un archivo "simulator" junto al ejecutable del panel (así queda en Docker).
//  3. En desarrollo (go run ./cmd/dashboard desde ingest-go/): lo compila una vez.
//     Si cambias el código del simulador, reinicia el panel para recompilarlo.
func (r *simRunner) resolve() (string, error) {
	r.mu.Lock()
	bin := r.bin
	r.mu.Unlock()
	if bin != "" {
		if !isFile(bin) {
			return "", fmt.Errorf("el simulador %s no existe (revisa SIMULATOR_BIN)", bin)
		}
		return bin, nil
	}

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "simulator"+suffix); isFile(p) {
			r.setBin(p)
			return p, nil
		}
	}

	if info, err := os.Stat(filepath.Join("cmd", "simulator")); err == nil && info.IsDir() {
		out := filepath.Join(os.TempDir(), "gamestats-simulator"+suffix)
		r.addLine("compilando el simulador (solo la primera vez)...")
		output, err := exec.Command("go", "build", "-o", out, "./cmd/simulator").CombinedOutput()
		if err != nil {
			for _, l := range strings.Split(strings.TrimSpace(string(output)), "\n") {
				r.addLine(l)
			}
			return "", fmt.Errorf("no se pudo compilar el simulador: %v", err)
		}
		r.setBin(out)
		return out, nil
	}
	return "", errors.New("no encontré el simulador: corre el panel desde la carpeta ingest-go o define SIMULATOR_BIN")
}

func (r *simRunner) setBin(p string) {
	r.mu.Lock()
	r.bin = p
	r.mu.Unlock()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// ---- HTTP ----

func (r *simRunner) handleState(w http.ResponseWriter, req *http.Request) {
	since, _ := strconv.ParseInt(req.URL.Query().Get("since"), 10, 64)
	state, lines, last := r.Snapshot(since)
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "lines": lines, "lastSeq": last})
}

func (r *simRunner) handleStart(w http.ResponseWriter, req *http.Request) {
	var q simRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&q); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "petición inválida: " + err.Error()})
		return
	}
	switch err := r.Start(q); {
	case errors.Is(err, errBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		state, _, _ := r.Snapshot(0)
		writeJSON(w, http.StatusAccepted, map[string]any{"state": state})
	}
}

func (r *simRunner) handleStop(w http.ResponseWriter, _ *http.Request) {
	r.Stop()
	state, _, _ := r.Snapshot(0)
	writeJSON(w, http.StatusAccepted, map[string]any{"state": state})
}
