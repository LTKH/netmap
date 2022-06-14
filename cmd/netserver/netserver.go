package main

import (
    "net/http"
    "time"
    "log"
    "os"
    "os/signal"
    "syscall"
    "flag"
    "gopkg.in/natefinch/lumberjack.v2"
    //"github.com/prometheus/client_golang/prometheus"
    //"github.com/prometheus/client_golang/prometheus/promhttp"
    //"github.com/ltkh/netmap/internal/db"
    "github.com/ltkh/netmap/internal/api/v1"
    "github.com/ltkh/netmap/internal/config"
)

func main() {

    // Command-line flag parsing
	lsAddress      := flag.String("httpListenAddr", ":8082", "listen address")
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

    // Enabled listen port
    //http.Handle("/metrics", promhttp.Handler())
    //http.HandleFunc("/-/healthy", apiV1.ApiHealthy)
    http.HandleFunc("/api/v1/netmap/netstat", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/records", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/status", apiV1.ApiRecords)
    http.HandleFunc("/api/v1/netmap/webhook", apiV1.ApiWebhook)

    go func(cfg *config.Global){
        if cfg.CertFile != "" && cfg.CertKey != "" {
            if err := http.ListenAndServeTLS(*lsAddress, cfg.CertFile, cfg.CertKey, nil); err != nil {
                log.Fatalf("[error] %v", err)
            }
        } else {
            if err := http.ListenAndServe(*lsAddress, nil); err != nil {
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
        time.Sleep(600 * time.Second)
    }
}