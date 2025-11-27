package main

import (
    "flag"
    "fmt"
    "log"
    "net"
    "net/http"
    _ "net/http/pprof"
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

    pb "github.com/ltkh/netmap/internal/grpc"
    "google.golang.org/grpc"
)

var (
    Version = "unknown"
)

func main() {
    // Command-line flag parsing with environment variables
    clAddress       := flag.String("listen.client-address", getEnv("NETSERVER_CLIENT_ADDRESS", "0.0.0.0:8084"), "listen client address")
    prAddress       := flag.String("listen.peer-address", getEnv("NETSERVER_PEER_ADDRESS", "0.0.0.0:8085"), "listen peer address")
    initCluster     := flag.String("initial-cluster", getEnv("NETSERVER_INITIAL_CLUSTER", ""), "initial cluster nodes")
    connString      := flag.String("db.conn-string", getEnv("NETSERVER_DB_CONN_STRING", ""), "db connection string")
    cfFile          := flag.String("config.file", getEnv("NETSERVER_CONFIG_FILE", "config/config.yml"), "config file")
    lgFile          := flag.String("log.file", getEnv("NETSERVER_LOG_FILE", ""), "log file")
    logMaxSize      := flag.Int("log.max-size", getEnvInt("NETSERVER_LOG_MAX_SIZE", 1), "log max size")
    logMaxBackups   := flag.Int("log.max-backups", getEnvInt("NETSERVER_LOG_MAX_BACKUPS", 3), "log max backups")
    logMaxAge       := flag.Int("log.max-age", getEnvInt("NETSERVER_LOG_MAX_AGE", 10), "log max age")
    logCompress     := flag.Bool("log.compress", getEnvBool("NETSERVER_LOG_COMPRE SS", true), "log compress")
    logHTTPRequests := flag.Bool("log.http-requests", getEnvBool("NETSERVER_LOG_HTTP_REQUESTS", false), "enable HTTP request logging")
    version         := flag.Bool("version", false, "show netserver version")
    debug           := flag.Bool("debug", false, "debug mode")

    flag.Parse()

    // Show version and exit if requested
    if *version {
        fmt.Printf("%v\n", Version)
        return
    }

    // Logging settings
    if *lgFile != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *logMaxSize,
            MaxBackups: *logMaxBackups,
            MaxAge:     *logMaxAge,
            Compress:   *logCompress,
        })
    }

    // Load configuration file
    cfg, err := config.New(cfFile) // Here we pass the pointer to cfFile
    if err != nil {
        log.Fatalf("[error] %v", err)
    }
    if *connString != "" {
        cfg.DB.ConnString = *connString
    }

    // Creating DB client
    clientDB, err := db.NewClient(cfg.DB)
    if err != nil {
        log.Fatalf("[error] %v", err)
    }

    // Initial cluster nodes
    peers := []string{}
    if *initCluster != "" {
        peers = strings.Split(*initCluster, ",")
    }

    // Creating gRPC
    srvV1 := v1.NewServer(&clientDB)
    grpcServer := grpc.NewServer()
    pb.RegisterEventServiceServer(grpcServer, srvV1)

    // Creating a port for gRPC
    lis, err := net.Listen("tcp", *prAddress)
    if err != nil {
        log.Fatalf("[error] failed to listen: %v", err)
    }

    go func() {
        if err := grpcServer.Serve(lis); err != nil {
            log.Fatalf("[error] failed to serve: %v", err)
        }
    }()

    // Creating API
    apiV1, err := v1.NewAPI(*debug, cfg, peers, clientDB, srvV1)
    if err != nil {
        log.Fatalf("[error] %v", err)
    }

    // Defining API paths
    mux := http.NewServeMux()
    mux.HandleFunc("/-/healthy", apiV1.ApiHealthy)
    mux.HandleFunc("/api/v1/netmap/status", apiV1.ApiStatus)
    mux.HandleFunc("/api/v1/netmap/netstat", apiV1.ApiNetstat)
    mux.HandleFunc("/api/v1/netmap/tracert", apiV1.ApiTracert)
    mux.HandleFunc("/api/v1/netmap/records", apiV1.ApiRecords)
    mux.HandleFunc("/api/v1/netmap/webhook", apiV1.ApiWebhook)
    mux.HandleFunc("/api/v1/netmap/exceptions", apiV1.ApiExceptions)
    mux.Handle("/metrics", promhttp.Handler())

    // Enable logging middleware only if logHTTPRequests is enabled
    var handler http.Handler = mux
    if *logHTTPRequests {
        handler = loggingMiddleware(mux)
    }

    // Creating a port for API
    go func(cfg *config.Global) {
        if cfg.CertFile != "" && cfg.CertKey != "" {
            if err := http.ListenAndServeTLS(*clAddress, cfg.CertFile, cfg.CertKey, handler); err != nil {
                log.Fatalf("[error] failed to listen: %v", err)
            }
        } else {
            if err := http.ListenAndServe(*clAddress, handler); err != nil {
                log.Fatalf("[error] failed to listen: %v", err)
            }
        }
    }(cfg.Global)

    log.Print("[info] netserver started -_^")

    // Program completion signal processing
    c := make(chan os.Signal, 2)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    for {
        <-c
        grpcServer.Stop()
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
