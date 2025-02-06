package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/rpc"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	v1 "github.com/ltkh/netmap/internal/api/v1"
	"github.com/ltkh/netmap/internal/config"
	"github.com/ltkh/netmap/internal/db"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Version = "unknown"
)

func main() {
	// Command-line flag parsing with environment variables
	var clAddress, prAddress, initCluster, connString, cfFile, lgFile string
	var logMaxSize, logMaxBackups, logMaxAge int
	var logCompress, version, logHTTPRequests bool

	flag.StringVar(&clAddress, "listen.client-address", getEnv("NETSERVER_CLIENT_ADDRESS", "127.0.0.1:8084"), "listen client address")
	flag.StringVar(&prAddress, "listen.peer-address", getEnv("NETSERVER_PEER_ADDRESS", "127.0.0.1:8085"), "listen peer address")
	flag.StringVar(&initCluster, "initial-cluster", getEnv("NETSERVER_INITIAL_CLUSTER", ""), "initial cluster nodes")
	flag.StringVar(&connString, "db.conn-string", getEnv("NETSERVER_DB_CONN_STRING", ""), "db connection string")
	flag.StringVar(&cfFile, "config.file", getEnv("NETSERVER_CONFIG_FILE", "config/config.yml"), "config file")
	flag.StringVar(&lgFile, "log.file", getEnv("NETSERVER_LOG_FILE", ""), "log file")
	flag.IntVar(&logMaxSize, "log.max-size", getEnvInt("NETSERVER_LOG_MAX_SIZE", 1), "log max size")
	flag.IntVar(&logMaxBackups, "log.max-backups", getEnvInt("NETSERVER_LOG_MAX_BACKUPS", 3), "log max backups")
	flag.IntVar(&logMaxAge, "log.max-age", getEnvInt("NETSERVER_LOG_MAX_AGE", 10), "log max age")
	flag.BoolVar(&logCompress, "log.compress", getEnvBool("NETSERVER_LOG_COMPRESS", true), "log compress")
	flag.BoolVar(&logHTTPRequests, "log.http-requests", getEnvBool("NETSERVER_LOG_HTTP_REQUESTS", false), "enable HTTP request logging")
	flag.BoolVar(&version, "version", false, "show netserver version")

	flag.Parse()

	// Show version and exit if requested
	if version {
		fmt.Printf("%v\n", Version)
		return
	}

	// Logging settings
	if lgFile != "" {
		log.SetOutput(&lumberjack.Logger{
			Filename:   lgFile,
			MaxSize:    logMaxSize,
			MaxBackups: logMaxBackups,
			MaxAge:     logMaxAge,
			Compress:   logCompress,
		})
	}

	// Load configuration file
	cfg, err := config.New(&cfFile) // Here we pass the pointer to cfFile
	if err != nil {
		log.Fatalf("[error] %v", err)
	}
	if connString != "" {
		cfg.DB.ConnString = connString
	}

	// Creating DB client
	clientDB, err := db.NewClient(cfg.DB)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}

	// Creating RPC
	rpcV1, err := v1.NewRPC(cfg, clientDB)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}

	// TCP Listen
	go func() {
		inbound, err := net.Listen("tcp", prAddress)
		if err != nil {
			log.Fatalf("[error] %v", err)
		}
		rpc.Register(rpcV1)
		rpc.Accept(inbound)
	}()

	// Initial cluster nodes
	peers := []string{}
	if initCluster != "" {
		peers = strings.Split(initCluster, ",")
	}
	if len(peers) == 0 && prAddress != "" {
		peers = append(peers, prAddress)
	}

	// Creating API
	apiV1, err := v1.NewAPI(cfg, peers, clientDB)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}

	// Enable logging middleware only if logHTTPRequests is enabled
	mux := http.NewServeMux()
	mux.HandleFunc("/-/healthy", apiV1.ApiStatus)
	mux.HandleFunc("/api/v1/netmap/status", apiV1.ApiStatus)
	mux.HandleFunc("/api/v1/netmap/netstat", apiV1.ApiNetstat)
	mux.HandleFunc("/api/v1/netmap/tracert", apiV1.ApiTracert)
	mux.HandleFunc("/api/v1/netmap/records", apiV1.ApiRecords)
	mux.HandleFunc("/api/v1/netmap/webhook", apiV1.ApiWebhook)
	mux.HandleFunc("/api/v1/netmap/exceptions", apiV1.ApiExceptions)
	mux.Handle("/metrics", promhttp.Handler())

	var handler http.Handler = mux
	if logHTTPRequests {
		handler = loggingMiddleware(mux)
	}

	go func(cfg *config.Global) {
		log.Printf("[info] listen client address: %v", clAddress)
		if cfg.CertFile != "" && cfg.CertKey != "" {
			if err := http.ListenAndServeTLS(clAddress, cfg.CertFile, cfg.CertKey, handler); err != nil {
				log.Fatalf("[error] %v", err)
			}
		} else {
			if err := http.ListenAndServe(clAddress, handler); err != nil {
				log.Fatalf("[error] %v", err)
			}
		}
	}(cfg.Global)

	go func() {
		for {
			apiV1.ApiPeers()
			time.Sleep(10 * time.Second)
		}
	}()

	log.Print("[info] netserver started -_^")

	// Program completion signal processing
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	for {
		<-c
		log.Print("[info] netserver stopped")
		os.Exit(0)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[request] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		duration := time.Since(start)
		log.Printf("[response] %s %s completed in %v", r.Method, r.URL.Path, duration)
	})
}

func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsedValue, err := strconv.Atoi(value)
	if err != nil {
		log.Printf("[warning] invalid int value for %s, using default: %d", key, defaultValue)
		return defaultValue
	}
	return parsedValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsedValue, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("[warning] invalid bool value for %s, using default: %v", key, defaultValue)
		return defaultValue
	}
	return parsedValue
}
