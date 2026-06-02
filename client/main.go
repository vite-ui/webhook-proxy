// Webhook proxy client — polls the remote proxy for queued requests,
// forwards them to a local service, and sends responses back.
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
	proxy := flag.String("proxy", "", "proxy server URL (e.g. https://xxx.code.run)")
	local := flag.String("local", "http://127.0.0.1:80", "local service to forward to")
	token := flag.String("token", "changeme", "auth token")
	flag.Parse()

	if *proxy == "" {
		log.Fatal("usage: proxy-client -proxy https://your-proxy.code.run -token <token>")
	}

	c := &http.Client{Timeout: 60 * time.Second}
	log.Printf("polling %s -> forwarding to %s", *proxy, *local)

	for {
		if err := poll(c, *proxy, *local, *token); err != nil {
			log.Printf("error: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}

func poll(c *http.Client, proxyURL, localURL, token string) error {
	req, _ := http.NewRequest("GET", proxyURL+"/api/poll", nil)
	req.Header.Set("X-Auth-Token", token)

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return nil
	}
	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized — check token")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("poll returned %d", resp.StatusCode)
	}

	id := resp.Header.Get("X-Msg-Id")
	method := resp.Header.Get("X-Msg-Method")
	path := resp.Header.Get("X-Msg-Path")
	body, _ := io.ReadAll(resp.Body)

	log.Printf("[%s] %s %s (%d bytes)", id, method, path, len(body))

	fwdReq, _ := http.NewRequest(method, localURL+path, bytes.NewReader(body))
	for k, vs := range resp.Header {
		if len(k) > 6 && k[:6] == "X-Fwd-" {
			for _, v := range vs {
				fwdReq.Header.Add(k[6:], v)
			}
		}
	}

	localResp, err := c.Do(fwdReq)
	if err != nil {
		return fmt.Errorf("[%s] forward: %w", id, err)
	}
	defer localResp.Body.Close()
	localBody, _ := io.ReadAll(localResp.Body)

	log.Printf("[%s] <- %d (%d bytes)", id, localResp.StatusCode, len(localBody))

	respReq, _ := http.NewRequest("POST", proxyURL+"/api/callback/"+id, bytes.NewReader(localBody))
	respReq.Header.Set("X-Auth-Token", token)
	respReq.Header.Set("X-Resp-Status", fmt.Sprintf("%d", localResp.StatusCode))
	for k, vs := range localResp.Header {
		for _, v := range vs {
			respReq.Header.Add("X-Fwd-"+k, v)
		}
	}

	rr, err := c.Do(respReq)
	if err != nil {
		return fmt.Errorf("[%s] callback: %w", id, err)
	}
	rr.Body.Close()
	return nil
}
