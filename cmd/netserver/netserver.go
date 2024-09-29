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
	"strings"
	"syscall"
	"time"

	v1 "github.com/ltkh/netmap/internal/api/v1"
	"github.com/ltkh/netmap/internal/config"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Version = "unknown"
)

func main() {
	var serverURL string

	if envURL := os.Getenv("NETSERVER_CLIENT_ADDRESS"); envURL != "" {
		serverURL = envURL
	} else {
		flag.StringVar(&serverURL, "listen.client-address", "127.0.0.1:8084", "listen client address")
		flag.Parse()
	}

	var configFolder string

	if envFolder := os.Getenv("NETSERVER_CONFIG_FILE"); envFolder != "" {
		configFolder = envFolder
	} else {
		flag.StringVar(&configFolder, "config.file", "config/config.yml", "config file")
		flag.Parse()
	}

	// Command-line flag parsing
	prAddress := flag.String("listen.peer-address", "127.0.0.1:8085", "listen peer address")
	initCluster := flag.String("initial-cluster", "", "initial cluster nodes")
	connString := flag.String("db.conn-string", "", "db connection string")
	lgFile := flag.String("log.file", "", "log file")
	logMaxSize := flag.Int("log.max-size", 1, "log max size")
	logMaxBackups := flag.Int("log.max-backups", 3, "log max backups")
	logMaxAge := flag.Int("log.max-age", 10, "log max age")
	logCompress := flag.Bool("log.compress", true, "log compress")
	version := flag.Bool("version", false, "show netserver version")
	flag.Parse()

	// Show version
	if *version {
		fmt.Printf("%v\n", Version)
		return
	}

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
	cfg, err := config.New(configFolder)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}
	if *connString != "" {
		cfg.DB.ConnString = *connString
	}

	// Creating RPC
	rpcV1, err := v1.NewRPC(cfg)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}

	// TCP Listen
	go func() {
		inbound, err := net.Listen("tcp", *prAddress)
		if err != nil {
			log.Fatalf("[error] %v", err)
		}
		rpc.Register(rpcV1)
		rpc.Accept(inbound)
	}()

	// Initial cluster nodes
	peers := []string{}
	if *initCluster != "" {
		peers = strings.Split(*initCluster, ",")
	}
	if len(peers) == 0 && os.Getenv("NETSERVER_INITIAL_CLUSTER") != "" {
		peers = strings.Split(os.Getenv("NETSERVER_INITIAL_CLUSTER"), ",")
	}
	if len(peers) == 0 && *prAddress != "" {
		peers = append(peers, *prAddress)
	}

	// Creating API
	apiV1, err := v1.NewAPI(cfg, peers)
	if err != nil {
		log.Fatalf("[error] %v", err)
	}

	// Enabled listen port
	http.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/api/v1/netmap/status", apiV1.ApiStatus)   //Only update (if exists)
	http.HandleFunc("/api/v1/netmap/netstat", apiV1.ApiNetstat) //Only added (if not exists)
	http.HandleFunc("/api/v1/netmap/tracert", apiV1.ApiTracert) //Run command
	http.HandleFunc("/api/v1/netmap/records", apiV1.ApiRecords) //Write record
	http.HandleFunc("/api/v1/netmap/webhook", apiV1.ApiWebhook)
	http.HandleFunc("/api/v1/netmap/exceptions", apiV1.ApiExceptions)

	http.Handle("/metrics", promhttp.Handler())

	go func(cfg *config.Global) {
		if cfg.CertFile != "" && cfg.CertKey != "" {
			if err := http.ListenAndServeTLS(serverURL, cfg.CertFile, cfg.CertKey, nil); err != nil {
				log.Fatalf("[error] %v", err)
			}
		} else {
			if err := http.ListenAndServe(serverURL, nil); err != nil {
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

	// Program completion signal processing
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Print("[info] netserver stopped")
		os.Exit(0)
	}()

	log.Print("[info] netserver started -_^")

	// Daemon mode
	for {
		time.Sleep(600 * time.Second)
	}
}
