package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/web"
)

// buildWebServer parses web-specific flags and returns a ready-to-start server.
// The caller is responsible for calling server.Start() and server.Shutdown().
func buildWebServer(profile string, args []string, menuData web.MenuDataLoader) (*web.Server, error) {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	listenAddr := fs.String("listen", "127.0.0.1:8420", "Listen address for web server")
	readOnly := fs.Bool("read-only", false, "Run in read-only mode (input disabled)")
	token := fs.String("token", "", "Bearer token for API/WS access")
	pushEnabled := fs.Bool("push", false, "Enable web push notifications (auto-generates VAPID keys per profile)")
	pushVAPIDSubject := fs.String("push-vapid-subject", "mailto:agentdeck@localhost", "VAPID subject used for web push notifications")
	pushTestEvery := fs.Duration("push-test-every", 0, "Send periodic push test notifications at this interval (e.g. 10s, 1m); 0 disables")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck web [options]")
		fmt.Println()
		fmt.Println("Start the TUI with web UI server running alongside.")
		fmt.Println("Use --headless to run only the web server (no TUI).")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  agent-deck web")
		fmt.Println("  agent-deck web --headless")
		fmt.Println("  agent-deck -p work web --listen 127.0.0.1:9000")
		fmt.Println("  agent-deck web --read-only")
		fmt.Println("  agent-deck web --push")
		fmt.Println("  agent-deck web --push --push-test-every 10s")
	}

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		return nil, fmt.Errorf("flag parsing: %w", err)
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *pushTestEvery < 0 {
		return nil, fmt.Errorf("--push-test-every must be >= 0")
	}
	if *pushTestEvery > 0 && !*pushEnabled {
		return nil, fmt.Errorf("--push-test-every requires --push")
	}

	effectiveProfile := session.GetEffectiveProfile(profile)

	resolvedPushSubject := *pushVAPIDSubject
	resolvedPushPublic := ""
	resolvedPushPrivate := ""
	if *pushEnabled {
		var generated bool
		var err error
		resolvedPushPublic, resolvedPushPrivate, generated, err = web.EnsurePushVAPIDKeys(effectiveProfile, resolvedPushSubject)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare web push keys: %w", err)
		}
		if generated {
			fmt.Println("Push keys: generated new VAPID keypair for profile")
		} else {
			fmt.Println("Push keys: using existing VAPID keypair for profile")
		}
	}

	server := web.NewServer(web.Config{
		ListenAddr:          *listenAddr,
		Profile:             effectiveProfile,
		ReadOnly:            *readOnly,
		Token:               *token,
		MenuData:            menuData,
		PushVAPIDPublicKey:  resolvedPushPublic,
		PushVAPIDPrivateKey: resolvedPushPrivate,
		PushVAPIDSubject:    resolvedPushSubject,
		PushTestInterval:    *pushTestEvery,
	})

	return server, nil
}

// handleWebHeadless starts only the HTTP server without the TUI.
// This bypasses the recursion guard, allowing the full dashboard (with APIs)
// to run inside an agent-deck session or any headless environment.
func handleWebHeadless(profile string, args []string) {
	// Strip --headless from args (buildWebServer's FlagSet doesn't know it)
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--headless" && a != "-headless" {
			filtered = append(filtered, a)
		}
	}

	server, err := buildWebServer(profile, filtered, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("Agent Deck web server (headless): http://%s\n", server.Addr())
	fmt.Println("Press Ctrl+C to stop.")

	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}
}
