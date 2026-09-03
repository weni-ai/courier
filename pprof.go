package courier

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

func newPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	return mux
}

func checkStatusAuth(username, password string, w http.ResponseWriter, r *http.Request) bool {
	if username == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != username || pass != password {
		w.Header().Set("WWW-Authenticate", `Basic realm="Authenticate"`)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorised.\n"))
		return false
	}
	return true
}

func wrapStatusAuth(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkStatusAuth(username, password, w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackAddress(addr string) bool {
	if addr == "" || addr == "0.0.0.0" || addr == "::" || addr == "[::]" {
		return false
	}
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

func pprofCanStart(cfg *Config) bool {
	if !cfg.EnablePprof {
		return false
	}
	return cfg.StatusUsername != "" || isLoopbackAddress(cfg.PprofAddress)
}

func (s *server) startPprofServer() {
	if !s.config.EnablePprof {
		return
	}
	if !pprofCanStart(s.config) {
		logrus.WithFields(logrus.Fields{
			"comp":    "server",
			"address": s.config.PprofAddress,
			"port":    s.config.PprofPort,
		}).Error("pprof not started: StatusUsername is required when pprof is bound to a non-loopback address")
		return
	}

	addr := fmt.Sprintf("%s:%d", s.config.PprofAddress, s.config.PprofPort)
	s.pprofServer = &http.Server{
		Addr:        addr,
		Handler:     wrapStatusAuth(s.config.StatusUsername, s.config.StatusPassword, newPprofMux()),
		ReadTimeout: 30 * time.Second,
	}

	s.waitGroup.Add(1)
	go func() {
		defer s.waitGroup.Done()
		logrus.WithFields(logrus.Fields{
			"comp":  "server",
			"addr":  addr,
			"state": "started",
		}).Info("pprof listening on ", addr)
		err := s.pprofServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logrus.WithFields(logrus.Fields{
				"comp":  "server",
				"state": "stopping",
				"err":   err,
			}).Error("pprof server error")
		}
	}()
}
