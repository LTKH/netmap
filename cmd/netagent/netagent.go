package main

import (
    "log"
    "time"
    "os"
    "math/rand"
    "os/signal"
    "syscall"
    "runtime"
    "flag"
    "net"
    "fmt"
    "os/exec"
    "sync"
    "context"
    "bytes"
    "encoding/json"
    "text/template"
    "github.com/pkg/errors"
    "github.com/naoina/toml"
    "gopkg.in/natefinch/lumberjack.v2"
    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/netstat"
)

var (
    httpClient = client.NewHttpClient()
    cacheRecords = cache.NewCacheRecords(10000)
)

type Records struct {
    sync.RWMutex
    items          map[string]config.SockTable
    limit          int
}

type Config struct {
    Global           *Global                 `toml:"global"`
    Netstat          *Netstat                `toml:"netstat"`
    Connections      *Connection             `toml:"connections"`
}

type Global struct {
    URLs             []string                `toml:"urls"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Interval         string                  `toml:"interval"`
    Timeout          string                  `toml:"timeout"`
    MaxRespTime      string                  `toml:"max_resp_time"`
    AccountID        uint32                  `toml:"account_id"`
}

type Netstat struct {
    URLs             []string                `toml:"urls"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Status           string                  `toml:"status"`
    Incoming         bool                    `toml:"incoming"`
    IgnoreHosts      []string                `toml:"ignore_hosts"`
    Interval         string                  `toml:"interval"`
    Timeout          string                  `toml:"timeout"`
    MaxRespTime      string                  `toml:"max_resp_time"`
}

type Connection struct {
    URLs             []string                `toml:"urls"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Command          string                  `toml:"command"`
    Interval         string                  `toml:"interval"`
    Timeout          string                  `toml:"timeout"`
    MaxRespTime      string                  `toml:"max_resp_time"`
}

type NetResponse struct {
    Address          string                  `json:"address"`
    Timeout          time.Duration           `json:"timeout"`
    Protocol         string                  `json:"protocol"`
}

type Alert struct {
    Status           string                  `json:"status,omitempty"`
    Labels           map[string]string       `json:"labels"`
    Annotations      Annotations             `json:"annotations"`
}

type Annotations struct {
    Description      string                  `json:"description"`
}

type ExceptionData struct {
    Data             []config.Exception      `json:"data"`
}

func randURLs(urls []string) []string {
    rand.Seed(time.Now().UnixNano())
    rand.Shuffle(len(urls), func(i, j int) { urls[i], urls[j] = urls[j], urls[i] })
    return urls
}

func dialTimeout(network, address string, timeout time.Duration) (int, float64) {
    // Set default values
    if timeout == 0 {
        timeout = 5 
    }
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout(network, address, timeout)
    // Stop timer
    responseTime := time.Since(start).Seconds()
    // Handle error
    if err != nil {
        log.Printf("[error] %v", err)

        if e, ok := err.(net.Error); ok && e.Timeout() {
            return 1, responseTime
        }
        return 2, responseTime
    }
    
    defer conn.Close()
    
    return 0, responseTime
}

func runCommand(scmd string, timeout time.Duration) ([]byte, float64, error) {
    log.Printf("[info] running '%s'", scmd)
    // Start Timer
    start := time.Now()
    // Create a new context and add a timeout to it
    ctx, cancel := context.WithTimeout(context.Background(), timeout * time.Second)
    defer cancel() // The cancel should be deferred so resources are cleaned up

    // Create the command with our context
    var cmd *exec.Cmd
    if runtime.GOOS == "windows" {
        cmd = exec.CommandContext(ctx, "cmd", "/C", scmd)
    } else {
        cmd = exec.CommandContext(ctx, "/bin/sh", "-c", scmd)
    }

    // This time we can simply use Output() to get the result.
    out, err := cmd.Output()

    // Stop timer
    responseTime := time.Since(start).Seconds()

    // Check the context error to see if the timeout was executed
    if ctx.Err() == context.DeadlineExceeded {
        return nil, responseTime, fmt.Errorf("command timed out '%s'", scmd)
    }

    // If there's no context error, we know the command completed (or errored).
    if err != nil {
        return nil, responseTime, fmt.Errorf("non-zero exit code: %v '%s'", err, scmd)
    }

    log.Printf("[info] finished '%s'", scmd)
    return out, responseTime, nil
}

func newTemplate(cmd string, tags map[string]string) string {

    var tpl bytes.Buffer

    funcMap := template.FuncMap{
        "hostname":  os.Hostname,
    }

    tmpl, err := template.New("new").Funcs(funcMap).Parse(cmd)
    if err != nil {
        log.Printf("[error] %v", errors.Wrap(err, "parse"))
        return tpl.String()
    }

    if err = tmpl.Execute(&tpl, &tags); err != nil {
        log.Printf("[error] %v", errors.Wrap(err, "execute"))
        return tpl.String()
    }

    return tpl.String()
}

func runTrace(cmd string, tags map[string]string, cfg client.HttpConfig) {

    var tpl bytes.Buffer

    tmpl, err := template.New("new").Parse(cmd)
    if err != nil {
        log.Printf("[error] %v", errors.Wrap(err, "parse"))
        return
    }

    if err = tmpl.Execute(&tpl, &tags); err != nil {
        log.Printf("[error] %v", errors.Wrap(err, "execute"))
        return
    }

    out, _, err := runCommand(tpl.String(), 300)
    if err != nil {
        log.Printf("[error] %v", err)
        return
    }

    var dt []Alert
    var al Alert

    al.Labels = tags
    al.Labels["alertname"] = "netmapTraceroute"
    al.Annotations.Description = string(out)

    dt = append(dt, al)

    jsn, err := json.Marshal(dt)
    if err != nil {
        log.Printf("[error] %v", err)
        return
    }

    if err := httpClient.WriteRecords(cfg, "/api/v1/netmap/webhook", jsn); err != nil {
        log.Printf("[error] %v", err)
    }

    return
}

// Get connections
func getConnections(cfg Config, hname string, debug bool){

    // Set default URLs
    if len(cfg.Connections.URLs) == 0 {
        cfg.Connections.URLs = cfg.Global.URLs
    }
    if len(cfg.Connections.URLs) == 0 {
        return
    }

    // Set Timeout
    if cfg.Connections.Timeout == "" {
        cfg.Connections.Timeout = cfg.Global.Timeout
    }
    cnTimeout, _ := time.ParseDuration(cfg.Connections.Timeout)
    if cnTimeout == 0 {
        log.Fatal("[error] setting connection timeout: invalid duration")
    }

    // Set MaxRespTime
    if cfg.Connections.MaxRespTime == "" {
        cfg.Connections.MaxRespTime = cfg.Global.MaxRespTime
    }
    cnMaxRespTime, _ := time.ParseDuration(cfg.Connections.MaxRespTime)
    if cnMaxRespTime == 0 {
        log.Fatal("[error] setting connection max_resp_time: invalid duration")
    }

    // Get connections
    clnt := client.HttpConfig{
        URLs: randURLs(cfg.Connections.URLs),
        ContentEncoding: cfg.Global.ContentEncoding,
    }

    body, err := httpClient.ReadRecords(clnt, fmt.Sprintf("/api/v1/netmap/records?src_name=%s", hname))
    if err != nil {
        log.Printf("[error] %v - /api/v1/netmap/records?src_name=%s", err, hname)
        return
    }

    var nrs netstat.NetstatData
    err = json.Unmarshal(body, &nrs)
    if err != nil {
        log.Printf("[error] %v - /api/v1/netmap/records?src_name=%s", err, hname)
        return
    }

    if debug {
        log.Printf("[debug] GET - /api/v1/netmap/records?src_name=%s (%v)", hname, len(nrs.Data))
    }   

    if debug {
        for _, nr := range nrs.Data {
            log.Printf(
                "[debug] record name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f,status=%s", 
                nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response, nr.Options.Status,
            )
        }
    }

    timestamp := time.Now().UTC().Unix()

    for _, nr := range nrs.Data {
        if nr.Options.Command == "" {
            nr.Options.Command = cfg.Connections.Command
        }

        if nr.Options.Timeout == 0 {
            nr.Options.Timeout = float64(cnTimeout / time.Second)
        }

        if nr.Options.MaxRespTime == 0 {
            nr.Options.MaxRespTime = float64(cnMaxRespTime / time.Second)
        }

        err := cacheRecords.Set(config.GetIdRec(&nr), nr, timestamp)
        if err != nil {
            log.Printf("[error] %v", err)
        }
    }

    count := cacheRecords.DelExpiredItems(timestamp)
    if debug {
        log.Printf("[debug] removed old records from cache (%d)", count)
    }
}

func main() {

    // Limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    // Command-line flag parsing
    cfFile         := flag.String("config.file", "config/netmap.toml", "config file")
    interval       := flag.Int("interval", 30, "interval")
    plugin         := flag.String("plugin", "", "plugin")
    lgFile         := flag.String("log.file", "", "log file")
    logMaxSize     := flag.Int("log.max-size", 1, "log max size") 
    logMaxBackups  := flag.Int("log.max-backups", 3, "log max backups")
    logMaxAge      := flag.Int("log.max-age", 10, "log max age")
    logCompress    := flag.Bool("log.compress", true, "log compress")
    debug          := flag.Bool("debug", false, "debug mode")
    flag.Parse()

    // Logging settings
    if *lgFile != "" || *plugin != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *logMaxSize,     // megabytes after which new file is created
            MaxBackups: *logMaxBackups,  // number of backups
            MaxAge:     *logMaxAge,      // days
            Compress:   *logCompress,    // using gzip
        })
    }

    // Loading configuration file
    f, err := os.Open(*cfFile)
    if err != nil {
        log.Fatalf("[error] %v", err)
    }
    var cfg Config
    if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
        log.Fatalf("[error] %v", err)
    }
    f.Close()

    // Set default Timeout
    if cfg.Global.Timeout == "" {
        cfg.Global.Timeout = "5s"
    }

    // Set default MaxRespTime
    if cfg.Global.MaxRespTime == "" {
        cfg.Global.MaxRespTime = "10s"
    }

    // Set default Interval
    if cfg.Global.Interval == "" {
        cfg.Global.Interval = "60s"
    }
    globalInterval, _ := time.ParseDuration(cfg.Global.Interval)
    if globalInterval == 0 {
        log.Fatal("[error] setting global interval: invalid duration")
    }

    // Set Interval
    if cfg.Connections.Interval == "" {
        cfg.Connections.Interval = cfg.Global.Interval
    }
    connectionsInterval, _ := time.ParseDuration(cfg.Connections.Interval)
    if connectionsInterval == 0 {
        log.Fatal("[error] setting connection interval: invalid duration")
    }

    // Get hostname
    hname, err := netstat.Hostname()
    if err != nil {
        log.Fatalf("[error] %v", err)
    }

    run := true
    
    // Program signal processing
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
    go func(){
        for {
            s := <-c
            switch s {
                case syscall.SIGHUP:
                    run = true
                case syscall.SIGINT:
                    log.Print("[info] netmap stopped")
                    os.Exit(0)
                case syscall.SIGTERM:
                    log.Print("[info] netmap stopped")
                    os.Exit(0)
                default:
                    log.Print("[info] unknown signal received")
            }
        }
    }()

    log.Print("[info] netmap started -_-")

    // Сheck connections
    go func(){
        clnt := client.HttpConfig{
            URLs: randURLs(cfg.Global.URLs),
            ContentEncoding: cfg.Global.ContentEncoding,
        }

        for {
            getConnections(cfg, hname, *debug)

            var wg sync.WaitGroup
            var nrr netstat.NetstatData

            items := cacheRecords.Items()

            if *debug {
                log.Printf("[debug] check started, records in cache (%v)", len(items))
                for _, nr := range items {
                    log.Printf(
                        "[debug] cache name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f,status=%s", 
                        nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response, nr.Options.Status,
                    )
                }
            }

            // Get records
            for _, nr := range items {

                if nr.Options.Status == "disabled" {
                    continue
                }

                wg.Add(1)

                go func(nr config.SockTable) {
                    defer wg.Done()

                    result := 0
                    response := float64(0)
                    trace := nr.Relation.Trace

                    tags := map[string]string{
                        "src_name":   nr.LocalAddr.Name,
                        "src_ip":     nr.LocalAddr.IP.String(),
                        "dst_name":   nr.RemoteAddr.Name,
                        "dst_ip":     nr.RemoteAddr.IP.String(),
                        "port":       fmt.Sprintf("%v", nr.Relation.Port),
                        "mode":       nr.Relation.Mode,
                    }
                    timeout := time.Duration(nr.Options.Timeout) * time.Second

                    switch nr.Relation.Mode {

                        case "tcp","udp":
                            address := fmt.Sprintf("%v:%v", nr.RemoteAddr.IP.String(), nr.Relation.Port)
                            result, response = dialTimeout(nr.Relation.Mode, address, timeout)
        
                            if result == 1 || response >= nr.Options.MaxRespTime {
                                if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                    trace = 1
                                    go runTrace(nr.Options.Command, tags, clnt)
                                }
                            }

                        case "cmd":
                            cmd := newTemplate(nr.Relation.Command, tags)

                            if cmd != "" {
                                _, response, err = runCommand(cmd, timeout)
                                if err != nil || response >= nr.Options.MaxRespTime {
                                    result = 1
                                    if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                        trace = 1
                                        go runTrace(nr.Options.Command, tags, clnt)
                                    }
                                }
                                
                            }

                        default:
                            return
                    }

                    if result == 0 && response < nr.Options.MaxRespTime {
                        trace = 0
                    }

                    if nr.Relation.Response != response {
                        nr.Relation.Response = response
                    }

                    if nr.Options.Service == "" {
                        nr.Options.Service = "unknown"
                    }

                    if nr.Relation.Result != result || nr.Relation.Trace != trace || nr.Options.AccountID != cfg.Global.AccountID {
                        nr.Options.AccountID = cfg.Global.AccountID
                        nr.Relation.Result = result
                        nr.Relation.Trace = trace
                        nrr.Data = append(nrr.Data, nr)
                    }

                    if *plugin == "telegraf" || *plugin == "windows" {
                        fmt.Printf(
                            "netmap,src_name=%s,src_ip=%s,dst_name=%s,dst_ip=%s,service=%s,port=%d,mode=%s result_code=%d,response_time=%f\n", 
                            nr.LocalAddr.Name,
                            nr.LocalAddr.IP,
                            nr.RemoteAddr.Name,
                            nr.RemoteAddr.IP,
                            nr.Options.Service,
                            nr.Relation.Port,
                            nr.Relation.Mode,
                            nr.Relation.Result,
                            nr.Relation.Response,
                        )
                    }

                    err := cacheRecords.Set(config.GetIdRec(&nr), nr, time.Now().UTC().Unix())
                    if err != nil {
                        log.Printf("[error] %v", err)
                    }

                }(nr)

                wg.Wait()

            }

            if len(nrr.Data) > 0 {
                    
                // Create json
                jsn, err := json.Marshal(nrr)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    // Sending status
                    if err = httpClient.WriteRecords(clnt, "/api/v1/netmap/status", jsn); err != nil {
                        log.Printf("[error] %v", err)
                    }
                    if *debug {
                        log.Printf("[debug] POST - /api/v1/netmap/status (%v)", len(nrr.Data))
                        for _, nr := range nrr.Data {
                            log.Printf(
                                "[debug] status name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f,status=%s", 
                                nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response, nr.Options.Status,
                            )
                        }
                    }
                }
            }
            
            time.Sleep(connectionsInterval)
        }
    }()

    // Netstat run cmd
    go func(){

        getConnections(cfg, hname, *debug)

        // Set default URLs
        if len(cfg.Netstat.URLs) == 0 {
            cfg.Netstat.URLs = cfg.Global.URLs
        }
        if len(cfg.Netstat.URLs) == 0 {
            return
        }

        // Set default ContentEncoding
        if cfg.Netstat.ContentEncoding == "" {
            cfg.Netstat.ContentEncoding = cfg.Global.ContentEncoding
        }

        clnt := client.HttpConfig{
            URLs: randURLs(cfg.Netstat.URLs),
            ContentEncoding: cfg.Netstat.ContentEncoding,
        }

        // Set Interval
        if cfg.Netstat.Interval == "" {
            cfg.Netstat.Interval = cfg.Global.Interval
        }
        netstatInterval, _ := time.ParseDuration(cfg.Netstat.Interval)
        if netstatInterval == 0 {
            log.Fatal("[error] setting netstat interval: invalid duration")
        }

        // Set Timeout
        if cfg.Netstat.Timeout == "" {
            cfg.Netstat.Timeout = cfg.Global.Timeout
        }
        netstatTimeout, _ := time.ParseDuration(cfg.Netstat.Timeout)
        if netstatTimeout == 0 {
            log.Fatal("[error] setting netstat timeout: invalid duration")
        }

        // Set MaxRespTime
        if cfg.Netstat.MaxRespTime == "" {
            cfg.Netstat.MaxRespTime = cfg.Global.MaxRespTime
        }
        netstatMaxRespTime, _ := time.ParseDuration(cfg.Netstat.MaxRespTime)
        if netstatTimeout == 0 {
            log.Fatal("[error] setting netstat max_resp_time: invalid duration")
        }

        for {
            options := config.Options {
                Status:      cfg.Netstat.Status,
                Timeout:     float64(netstatTimeout / time.Second),
                MaxRespTime: float64(netstatMaxRespTime / time.Second),
                AccountID:   cfg.Global.AccountID,
            }

            // Get exceptions
            ihosts := cfg.Netstat.IgnoreHosts
            body, err := httpClient.ReadRecords(clnt, fmt.Sprintf("/api/v1/netmap/exceptions?account_id=%d", cfg.Global.AccountID))
            if err != nil {
                log.Printf("[error] %v - /api/v1/netmap/exceptions?account_id=%d", err, cfg.Global.AccountID)
            } else {

                var exp config.ExceptionData
                err = json.Unmarshal(body, &exp)
                if err != nil {
                    log.Printf("[error] %v - /api/v1/netmap/exceptions?account_id=%d", err, cfg.Global.AccountID)
                } else {

                    if *debug {
                        log.Printf("[debug] GET - /api/v1/netmap/exceptions (%v)", len(exp.Data))
                        for _, ex := range exp.Data {
                            log.Printf(
                                "[debug] exception accountID=%d,hostMask=%s,ignoreMask=%s", 
                                ex.AccountID, ex.HostMask, ex.IgnoreMask,
                            )
                        }
                    }

                    for _, ex := range exp.Data {
                        ihosts = append(ihosts, ex.IgnoreMask)
                    }

                    //for _, nr := range cacheRecords.Items() {
                    //    ihosts = append(ihosts, fmt.Sprintf("%v:%v", nr.RemoteAddr.Name, nr.Relation.Port))
                    //}

                    if *debug {
                        log.Print("[debug] netstat started")
                    }

                    nrs, err := netstat.GetSocks(ihosts, options, cfg.Netstat.Incoming, *debug)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        if len(nrs.Data) > 0 {
                            jsn, err := json.Marshal(nrs)
                            if err != nil {
                                log.Printf("[error] %v", err)
                            } else {
                                if err = httpClient.WriteRecords(clnt, "/api/v1/netmap/netstat", jsn); err != nil {
                                    log.Printf("[error] %v", err)
                                } else {
                                    if *debug {
                                        log.Printf("[debug] POST - /api/v1/netmap/netstat (%v)", len(nrs.Data))
                                        for _, nr := range nrs.Data {
                                            log.Printf(
                                                "[debug] netstat name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f,status=%s", 
                                                nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response, nr.Options.Status,
                                            )
                                        }
                                    }
                                }
                            }
                        }
                    }

                }
            }
            time.Sleep(netstatInterval)
        }
    }()

    // Daemon mode
    for (run) {
        if *plugin == "telegraf" {
            run = false
        }

        time.Sleep(time.Duration(*interval) * time.Second)
    }

}

