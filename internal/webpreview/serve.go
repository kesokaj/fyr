package webpreview

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Serve binds a foreground HTTP preview listener (127.0.0.1:0 by default),
// writes the tokenized preview URL(s), emits a warning on non-loopback binds,
// and shuts down cleanly when ctx is cancelled.
func Serve(ctx context.Context, assets map[string]Asset, opts Options) error {
	if ctx == nil {
		return errors.New("webpreview: nil context")
	}
	tok, err := generateToken(rand.Reader)
	if err != nil {
		return err
	}
	listenAddr := strings.TrimSpace(opts.Listen)
	if listenAddr == "" {
		listenAddr = defaultListen
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("webpreview: listen %q: %w", listenAddr, err)
	}
	defer func() {
		_ = ln.Close() // best-effort close; srv.Serve also closes ln on Shutdown
	}()
	outW, errW := resolveWriters(opts)
	urls, nonLoopback := previewURLs(listenAddr, ln.Addr(), tok)
	if nonLoopback {
		fmt.Fprintf(errW, "webpreview: warning: listening on non-loopback address %s; tokenized URL(s): %s\n",
			ln.Addr().String(), strings.Join(urls, ", "))
	}
	for _, u := range urls {
		fmt.Fprintln(outW, u)
	}
	return runServer(ctx, ln, NewHandler(tok, assets, opts.DefaultCSP))
}

func resolveWriters(opts Options) (outW, errW io.Writer) {
	switch {
	case opts.Out != nil && opts.Err != nil:
		return opts.Out, opts.Err
	case opts.Out != nil:
		return opts.Out, opts.Out
	case opts.Err != nil:
		return io.Discard, opts.Err
	default:
		return os.Stdout, os.Stderr
	}
}

func runServer(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	errc := make(chan error, 1)
	go func() {
		errc <- srv.Serve(ln)
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("webpreview: serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	serveErr := <-errc
	if shutdownErr != nil {
		return fmt.Errorf("webpreview: shutdown: %w", shutdownErr)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("webpreview: serve: %w", serveErr)
	}
	return nil
}

func previewURLs(requested string, addr net.Addr, token string) (urls []string, nonLoopback bool) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return []string{formatPreviewURL(addr.String(), token)}, true
	}
	port := strconv.Itoa(tcpAddr.Port)
	ip := tcpAddr.IP
	if ip.IsLoopback() {
		return []string{formatPreviewURL(net.JoinHostPort(ip.String(), port), token)}, false
	}
	if !ip.IsUnspecified() {
		return []string{formatPreviewURL(net.JoinHostPort(ip.String(), port), token)}, true
	}
	localHost := "127.0.0.1"
	if strings.HasPrefix(requested, "[::]") {
		localHost = "::1"
	}
	urls = append(urls, formatPreviewURL(net.JoinHostPort(localHost, port), token))
	for _, host := range interfaceIPs(ip.To4() != nil) {
		urls = append(urls, formatPreviewURL(net.JoinHostPort(host, port), token))
	}
	return urls, true
}

func interfaceIPs(v4Only bool) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP == nil || !ipNet.IP.IsGlobalUnicast() {
			continue
		}
		if v4Only && ipNet.IP.To4() == nil {
			continue
		}
		s := ipNet.IP.String()
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func formatPreviewURL(hostPort, token string) string {
	return "http://" + hostPort + "/" + token + "/"
}
