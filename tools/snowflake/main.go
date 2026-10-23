// Command snowflake-helper runs a Snowflake proxy: it lets people in
// censored countries reach Tor through this computer. Albatross starts it
// only when the user turns on "Help others" and the computer is outside
// Iran (see internal/engine/help.go). It lives in its own module because the
// Snowflake library needs a newer Go than the app.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	sf "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/snowflake/v2/proxy/lib"
)

func main() {
	capacity := flag.Uint("capacity", 4, "most people helped at once")
	flag.Parse()
	p := &sf.SnowflakeProxy{Capacity: *capacity, ProxyType: "albatross"}
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		p.Stop()
	}()
	log.Println("snowflake proxy starting")
	if err := p.Start(); err != nil {
		log.Fatal(err)
	}
}
