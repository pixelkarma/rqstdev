package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"rqstdev/api/internal/config"
	"rqstdev/api/internal/store"
)

type backendRoute struct {
	VMName      string
	BackendPort int
}

type publicListener struct {
	server   *http.Server
	listener net.Listener
}

type routeManager struct {
	cfg             config.Config
	store           *store.Store
	logger          *log.Logger
	apiHost         string
	defaultRoutes   map[string]backendRoute
	publishedRoutes map[int]map[string]backendRoute
	listeners       map[int]*publicListener
	mu              sync.RWMutex
	transport       *http.Transport
}

func newRouteManager(cfg config.Config, st *store.Store, logger *log.Logger) *routeManager {
	return &routeManager{
		cfg:             cfg,
		store:           st,
		logger:          logger,
		apiHost:         hostOnly(cfg.BaseURL),
		defaultRoutes:   map[string]backendRoute{},
		publishedRoutes: map[int]map[string]backendRoute{},
		listeners:       map[int]*publicListener{},
		transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

func (rm *routeManager) start(ctx context.Context) error {
	vms, err := rm.store.AllVMs(ctx)
	if err != nil {
		return err
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.defaultRoutes = map[string]backendRoute{}
	rm.publishedRoutes = map[int]map[string]backendRoute{}

	for _, vm := range vms {
		rm.defaultRoutes[rm.vmHost(vm.Name)] = backendRoute{VMName: vm.Name, BackendPort: vm.HostWebPort}
		ports, err := rm.store.PublishedPortsForVM(ctx, vm.ID)
		if err != nil {
			return err
		}
		for _, port := range ports {
			if rm.publishedRoutes[port.PublicPort] == nil {
				rm.publishedRoutes[port.PublicPort] = map[string]backendRoute{}
			}
			rm.publishedRoutes[port.PublicPort][rm.vmHost(vm.Name)] = backendRoute{VMName: vm.Name, BackendPort: port.BackendPort}
		}
	}

	for publicPort := range rm.publishedRoutes {
		if err := rm.ensureListenerLocked(publicPort); err != nil {
			return err
		}
	}
	return nil
}

func (rm *routeManager) shutdown(ctx context.Context) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var firstErr error
	for port, listener := range rm.listeners {
		if err := listener.server.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("shutdown port %d listener: %w", port, err)
		}
	}
	rm.listeners = map[int]*publicListener{}
	return firstErr
}

func (rm *routeManager) addVM(vm store.VM) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.defaultRoutes[rm.vmHost(vm.Name)] = backendRoute{VMName: vm.Name, BackendPort: vm.HostWebPort}
}

func (rm *routeManager) removeVM(vm store.VM, ports []store.PublishedPort) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.defaultRoutes, rm.vmHost(vm.Name))
	for _, port := range ports {
		if routes := rm.publishedRoutes[port.PublicPort]; routes != nil {
			delete(routes, rm.vmHost(vm.Name))
			if len(routes) == 0 {
				if err := rm.stopListenerLocked(port.PublicPort); err != nil {
					return err
				}
				delete(rm.publishedRoutes, port.PublicPort)
			}
		}
	}
	return nil
}

func (rm *routeManager) addPublishedPort(vm store.VM, port store.PublishedPort) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.publishedRoutes[port.PublicPort] == nil {
		rm.publishedRoutes[port.PublicPort] = map[string]backendRoute{}
	}
	rm.publishedRoutes[port.PublicPort][rm.vmHost(vm.Name)] = backendRoute{VMName: vm.Name, BackendPort: port.BackendPort}
	return rm.ensureListenerLocked(port.PublicPort)
}

func (rm *routeManager) removePublishedPort(vm store.VM, port store.PublishedPort) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	routes := rm.publishedRoutes[port.PublicPort]
	if routes == nil {
		return nil
	}
	delete(routes, rm.vmHost(vm.Name))
	if len(routes) == 0 {
		if err := rm.stopListenerLocked(port.PublicPort); err != nil {
			return err
		}
		delete(rm.publishedRoutes, port.PublicPort)
	}
	return nil
}

func (rm *routeManager) serveRoutedRequest(w http.ResponseWriter, r *http.Request, fixedPort int) bool {
	host := normalizeHost(r.Host)
	if host == "" || host == rm.apiHost {
		return false
	}

	publicPort := fixedPort
	if publicPort == 0 {
		publicPort = requestPort(r)
	}

	rm.mu.RLock()
	route, ok := rm.lookupLocked(host, publicPort)
	rm.mu.RUnlock()
	if !ok {
		http.Error(w, "route not found", http.StatusNotFound)
		return true
	}

	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", route.BackendPort)}
	proxy := &httputil.ReverseProxy{
		Transport: rm.transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
			if target.RawQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = target.RawQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = target.RawQuery + "&" + req.URL.RawQuery
			}
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Forwarded-Port", strconv.Itoa(publicPort))
			if req.Header.Get("X-Forwarded-Proto") == "" {
				req.Header.Set("X-Forwarded-Proto", forwardedProto(r, publicPort))
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
	return true
}

func (rm *routeManager) lookupLocked(host string, publicPort int) (backendRoute, bool) {
	if publicPort == 80 || publicPort == 443 {
		route, ok := rm.defaultRoutes[host]
		return route, ok
	}
	routes, ok := rm.publishedRoutes[publicPort]
	if !ok {
		return backendRoute{}, false
	}
	route, ok := routes[host]
	return route, ok
}

func (rm *routeManager) ensureListenerLocked(publicPort int) error {
	if _, ok := rm.listeners[publicPort]; ok {
		return nil
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(publicPort))
	if err != nil {
		return fmt.Errorf("listen on published port %d: %w", publicPort, err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rm.serveRoutedRequest(w, r, publicPort) {
			return
		}
		http.Error(w, "route not found", http.StatusNotFound)
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	rm.listeners[publicPort] = &publicListener{server: server, listener: listener}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			rm.logger.Printf("published-port listener %d failed: %v", publicPort, err)
		}
	}()
	return nil
}

func (rm *routeManager) stopListenerLocked(publicPort int) error {
	listener := rm.listeners[publicPort]
	if listener == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := listener.server.Shutdown(ctx); err != nil {
		return err
	}
	delete(rm.listeners, publicPort)
	return nil
}

func (rm *routeManager) vmHost(vmName string) string {
	return strings.ToLower(vmName + "." + rm.cfg.BaseDomain)
}

func requestPort(r *http.Request) int {
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-Port")); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			return port
		}
	}
	host := strings.TrimSpace(r.Host)
	if strings.Contains(host, ":") {
		if _, portValue, err := net.SplitHostPort(host); err == nil {
			if port, err := strconv.Atoi(portValue); err == nil {
				return port
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return 443
	}
	return 80
}

func forwardedProto(r *http.Request, publicPort int) string {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") || publicPort == 443 {
		return "https"
	}
	return "http"
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, ":") {
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
	}
	return strings.TrimSuffix(strings.ToLower(value), ".")
}

func hostOnly(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return normalizeHost(parsed.Host)
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}
