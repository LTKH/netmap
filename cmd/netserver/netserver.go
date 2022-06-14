package main

import (
    "net/http"
    "time"
    "log"
    "os"
    "os/signal"
    "syscall"
    "flag"
    "net"
    "sync/atomic"
    "gopkg.in/natefinch/lumberjack.v2"
    //"github.com/prometheus/client_golang/prometheus"
    //"github.com/prometheus/client_golang/prometheus/promhttp"
    //"github.com/ltkh/netmap/internal/db"
    "github.com/ltkh/netmap/internal/api/v1"
    "github.com/ltkh/netmap/internal/config"
)

//-------------------------------------------------
type ConnectionWatcher struct {
    n int64
}

// OnStateChange records open connections in response to connection
// state changes. Set net/http Server.ConnState to this method
// as value.
func (cw *ConnectionWatcher) OnStateChange(conn net.Conn, state http.ConnState) {
    switch state {
    case http.StateNew:
        cw.Add(1)
    case http.StateHijacked, http.StateClosed:
        cw.Add(-1)
    }
}

// Count returns the number of connections at the time
// the call.    
func (cw *ConnectionWatcher) Count() int {
    return int(atomic.LoadInt64(&cw.n))
}

// Add adds c to the number of active connections. 
func (cw *ConnectionWatcher) Add(c int64) {
    atomic.AddInt64(&cw.n, c)
}
//-------------------------------------------------

func main() {

    // Command-line flag parsing
    cfFile         := flag.String("config", "config/config.yml", "config file")
    lgFile         := flag.String("logfile", "", "log file")
    logMaxSize     := flag.Int("log.max-size", 1, "log max size") 
    logMaxBackups  := flag.Int("log.max-backups", 3, "log max backups")
    logMaxAge      := flag.Int("log.max-age", 10, "log max age")
    logCompress    := flag.Bool("log.compress", true, "log compress")
    flag.Parse()

    // Logging settings
    if *lgFile != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *logMaxSize,    // megabytes after which new file is created
            MaxBackups: *logMaxBackups, // number of backups
            MaxAge:     *logMaxAge,     // days
            Compress:   *logCompress,   // using gzip
        })
    }

    // Loading configuration file
    cfg, err := config.New(*cfFile)
    if err != nil {
        log.Fatalf("[error] %v", err)
    }

    // Creating api
    apiV1, err := v1.New(cfg)
    if err != nil {
        log.Fatalf("[error] %v", err)
    }

    var cw ConnectionWatcher
    server := &http.Server{
        Addr:      cfg.Global.Listen,
        ConnState: cw.OnStateChange,
    }

    // Enabled listen port
    //http.Handle("/metrics", promhttp.Handler())
    //http.HandleFunc("/-/healthy", apiV1.ApiHealthy)
    http.HandleFunc("/api/v1/netmap/netstat", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/records", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/status", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/webhook", apiV1.ApiWebhook)

    go func(cfg *config.Global){
        if cfg.CertFile != "" && cfg.CertKey != "" {
            //if err := server.ListenAndServeTLS(cfg.Listen, cfg.CertFile, cfg.CertKey, nil); err != nil {
            //    log.Fatalf("[error] %v", err)
            //}
        } else {
            //if err := server.ListenAndServe(cfg.Listen, nil); err != nil {
            //    log.Fatalf("[error] %v", err)
            //}
            if err := server.ListenAndServe(); err != nil {
                log.Fatalf("[error] %v", err)
            }
        }
    }(cfg.Global)

    // Program completion signal processing
    c := make(chan os.Signal, 2)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <- c
        log.Print("[info] netserver stopped")
        os.Exit(0)
    }()

    log.Print("[info] netserver started -_^")

    // Daemon mode
    for {
        apiV1.ApiDelExpiredItems()
        apiV1.ApiGetClusterRecords()
        time.Sleep(60 * time.Second)
        log.Printf("[info] count: %v", cw.Count())
    }
}