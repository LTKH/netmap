package main

import (
    "flag"
    "log"
    "net/http"
    "os"
    "os/signal"
    "runtime"
    "syscall"
    //"io/ioutil"
	//"gopkg.in/yaml.v2"
    //"github.com/go-chi/chi"
    "gopkg.in/natefinch/lumberjack.v2"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/api/v1"
)

func main() {

    //limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    //command-line flag parsing
    cfFile          := flag.String("config", "netmap-server.yml", "config file")
    lgFile          := flag.String("logfile", "", "log file")
    logMaxSize      := flag.Int("log.max-size", 1, "log max size") 
    logMaxBackups   := flag.Int("log.max-backups", 3, "log max backups")
    logMaxAge       := flag.Int("log.max-age", 10, "log max age")
    logCompress     := flag.Bool("log.compress", true, "log compress")
    flag.Parse()

	//program completion signal processing
    c := make(chan os.Signal, 2)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-c
        log.Print("[info] netmap-server stopped")
        os.Exit(0)
    }()

    //loading configuration file
    cfg, err := config.LoadConfigFile(*cfFile)
    if err != nil {
        log.Fatalf("[error] loading configuration file: %v", err)
    }

	//logging settings
    if *lgFile != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *logMaxSize,    // megabytes after which new file is created
            MaxBackups: *logMaxBackups, // number of backups
            MaxAge:     *logMaxAge,     // days
            Compress:   *logCompress,   // using gzip
        })
    }

    log.Print("[info] netmap-server started")

    mux := http.NewServeMux()
	mux.Handle("/api/v1/netmap/netstat", &v1.ApiNetstat{cfg.Databases})
    mux.Handle("/api/v1/netmap/traceroute", &v1.ApiTraceroute{cfg.Databases})
    mux.Handle("/api/v1/netmap/status", &v1.ApiStatus{cfg.Databases})
    mux.Handle("/api/v1/netmap/records", &v1.ApiRecords{cfg.Databases})
    
	http.ListenAndServe(cfg.Global.Listen, mux)

    //daemon mode
    //for {
    //
    //    time.Sleep(5 * time.Second)
    //}

}