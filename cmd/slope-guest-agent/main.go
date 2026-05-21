package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"slope/internal/guestagent"
)

func main() {
	configPath := flag.String("config", "", "config path; default is slope.conf next to the executable")
	addr := flag.String("addr", "", "listen address override")
	root := flag.String("root", "", "writable root override")
	allowRoots := flag.String("allow-roots", "", "semicolon-separated extra roots allowed for read/execute, e.g. C:\\Windows\\System32;C:\\symbols")
	maxBytes := flag.Int64("max-bytes", 0, "maximum file upload/download size override")
	flag.Parse()

	cfg, err := guestagent.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	if *root != "" {
		cfg.Root = *root
	}
	if *allowRoots != "" {
		cfg.AllowedRoots = guestagent.SplitRoots(*allowRoots)
	}
	if *maxBytes > 0 {
		cfg.MaxBytes = *maxBytes
	}

	policy, err := guestagent.NewPathPolicy(cfg.Root, cfg.AllowedRoots)
	if err != nil {
		log.Fatalf("path policy: %v", err)
	}
	if err := policy.EnsureWriteRoot(); err != nil {
		log.Fatalf("create root: %v", err)
	}
	srv := guestagent.NewServer(policy, cfg.MaxBytes)
	log.Printf("slope guest agent listening on %s root=%s allowed_roots=%s pid=%d", cfg.Addr, policy.WriteRoot, strings.Join(policy.AllowedRoots, ";"), os.Getpid())
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
