// Webhook forwarding proxy — queues incoming webhooks and allows
// a polling client to process them asynchronously.
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
	secret = os.Getenv("AUTH_TOKEN")
	if secret == "" {
		secret = "changeme"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/poll", handlePoll)
	mux.HandleFunc("/api/callback/", handleCallback)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", handleIncoming)

	log.Printf("proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), mux))
}

func handleIncoming(w http.ResponseWriter, r *http.Request) {
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

	log.Printf("[%d] queued %s %s (%d bytes)", id, r.Method, r.URL.Path, len(body))

	select {
	case resp := <-p.respCh:
		for k, vs := range resp.headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.status)
		w.Write(resp.body)
		log.Printf("[%d] responded %d (%d bytes)", id, resp.status, len(resp.body))
	case <-time.After(120 * time.Second):
		mu.Lock()
		delete(waiting, id)
		mu.Unlock()
		http.Error(w, "timeout", 504)
		log.Printf("[%d] timeout", id)
	}
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Auth-Token") != secret {
		http.Error(w, "unauthorized", 401)
		return
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
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

			w.Header().Set("X-Msg-Id", strconv.FormatUint(p.id, 10))
			w.Header().Set("X-Msg-Method", p.method)
			w.Header().Set("X-Msg-Path", p.path)
			for k, vs := range p.headers {
				for _, v := range vs {
					w.Header().Add("X-Fwd-"+k, v)
				}
			}
			w.WriteHeader(200)
			w.Write(p.body)
			return
		}
		mu.Unlock()
		time.Sleep(500 * time.Millisecond)
	}

	w.WriteHeader(204)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Auth-Token") != secret {
		http.Error(w, "unauthorized", 401)
		return
	}

	idStr := r.URL.Path[len("/api/callback/"):]
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	body, _ := io.ReadAll(r.Body)
	status, _ := strconv.Atoi(r.Header.Get("X-Resp-Status"))
	if status == 0 {
		status = 200
	}

	respHeaders := make(http.Header)
	for k, vs := range r.Header {
		if len(k) > 6 && k[:6] == "X-Fwd-" {
			for _, v := range vs {
				respHeaders.Add(k[6:], v)
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
		http.Error(w, "expired", 404)
		return
	}

	p.respCh <- &response{status: status, headers: respHeaders, body: body}
	w.Write([]byte("ok"))
}
