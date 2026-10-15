package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// runContinuous juega partidas nuevas sin parar hasta que pasa `duration`
// (o hasta que se presiona Ctrl+C). Mantiene como máximo `parallel` partidas
// en curso: cuando una termina, empieza otra.
//
// El límite se hace con un SEMÁFORO: un canal con buffer de tamaño `parallel`.
// Para empezar una partida hay que meter una ficha en el canal; si está lleno,
// hay que esperar a que alguna partida termine y saque la suya.
func runContinuous(s *sender, seed uint64, run string, duration time.Duration, parallel int, delay time.Duration, special bool) []planned {
	if parallel <= 0 {
		parallel = 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var queue []planned // las guionizadas van primero, si se pidieron
	if special {
		queue = plan(seed, run, 0, 0, 0, true)
	}

	log.Printf("modo continuo: %s, hasta %d partidas a la vez (Ctrl+C para terminar antes)", duration, parallel)
	slots := make(chan struct{}, parallel)
	progress := time.NewTicker(10 * time.Second)
	defer progress.Stop()

	var wg sync.WaitGroup
	var finished atomic.Int64
	var played []planned // solo la toca esta goroutine
	r := rand.New(rand.NewPCG(seed, 0xC0FFEE))

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-progress.C:
			log.Printf("partidas terminadas: %d · en curso: %d · eventos enviados: %d",
				finished.Load(), len(slots), s.sent.Load())
		case slots <- struct{}{}: // hay espacio: empieza otra partida
			var pm planned
			if len(queue) > 0 {
				pm, queue = queue[0], queue[1:]
			} else {
				pm = randomMatch(r, seed, run, len(played)+1)
			}
			played = append(played, pm)
			wg.Add(1)
			go func(pm planned, i int) {
				defer wg.Done()
				defer func() { <-slots }() // saca su ficha: libera el lugar
				playMatch(s, pm, rand.New(rand.NewPCG(seed, uint64(5000+i))), delay)
				finished.Add(1)
			}(pm, len(played))
		}
	}
	stop() // un segundo Ctrl+C ya corta el programa de inmediato

	log.Printf("tiempo cumplido: no empiezo más partidas; espero las %d en curso...", len(slots))
	wg.Wait()
	log.Printf("partidas jugadas en total: %d", len(played))
	return played
}

// randomMatch genera una partida al azar de cualquiera de los tres juegos.
func randomMatch(r *rand.Rand, seed uint64, run string, n int) planned {
	gr := rand.New(rand.NewPCG(seed, uint64(n)+100)) // azar propio de esta partida
	start := time.Now().UTC()
	switch r.IntN(3) {
	case 0:
		id := fmt.Sprintf("combat-%s-%03d", run, n)
		return planned{id, "combat", "al azar", simulateCombat(gr, id, start)}
	case 1:
		id := fmt.Sprintf("chase-%s-%03d", run, n)
		return planned{id, "chase", "al azar", simulateChase(gr, id, start, chaseNormal)}
	default:
		id := fmt.Sprintf("racing-%s-%03d", run, n)
		return planned{id, "racing", "al azar", simulateRace(gr, id, start, 0)}
	}
}
