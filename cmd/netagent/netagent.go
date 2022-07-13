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
    "github.com/ltkh/netmap/internal/netstat"
    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/client"
)

var (
    httpClient = client.NewHttpClient()
)

type Config struct {
    Global           *Global                 `toml:"global"`
    Netstat          *Netstat                `toml:"netstat"`
    Connections      []*Connection           `toml:"connections"`
}

type Global struct {
    URLs             []string                `toml:"urls"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Interval         string                  `toml:"interval"`
    Timeout          string                  `toml:"timeout"`
    MaxRespTime      string                  `toml:"max_resp_time"`
}

type Netstat struct {
    URLs             []string                `toml:"urls"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Status           string                  `toml:"status"`
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

    // Get hostname
    hname, err := netstat.Hostname()
    if err != nil {
        log.Fatal("[error] %v", err)
    }

    log.Print("[info] netmap started -_-")

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

    // Get connections
    for _, cn := range cfg.Connections {

        // Set default URLs
        if len(cn.URLs) == 0 {
            cn.URLs = cfg.Global.URLs
        }
        if len(cn.URLs) == 0 {
            continue
        }

        // Set default ContentEncoding
        if cn.ContentEncoding == "" {
            cn.ContentEncoding = cfg.Global.ContentEncoding
        }

        // Set Interval
        if cn.Interval == "" {
            cn.Interval = cfg.Global.Interval
        }
        cnInterval, _ := time.ParseDuration(cn.Interval)
        if cnInterval == 0 {
            log.Fatal("[error] setting connection interval: invalid duration")
        }

        // Set Timeout
        if cn.Timeout == "" {
            cn.Timeout = cfg.Global.Timeout
        }
        cnTimeout, _ := time.ParseDuration(cn.Timeout)
        if cnTimeout == 0 {
            log.Fatal("[error] setting connection timeout: invalid duration")
        }

        // Set MaxRespTime
        if cn.MaxRespTime == "" {
            cn.MaxRespTime = cfg.Global.MaxRespTime
        }
        cnMaxRespTime, _ := time.ParseDuration(cn.MaxRespTime)
        if cnMaxRespTime == 0 {
            log.Fatal("[error] setting connection max_resp_time: invalid duration")
        }

        // Check connections
        go func(cn *Connection) {

            config := client.HttpConfig{
                URLs: randURLs(cn.URLs),
                ContentEncoding: cn.ContentEncoding,
            }

            for {
                var nrs netstat.NetstatData

                body, err := httpClient.ReadRecords(config, fmt.Sprintf("/api/v1/netmap/records?src_name=%s", hname))
                if err == nil {

                    if err := json.Unmarshal(body, &nrs); err != nil {
                        log.Printf("[error] %v - /api/v1/netmap/records", err)
                    } else {

                        var wg sync.WaitGroup
                        var nrr netstat.NetstatData
                        
                        for _, nr := range nrs.Data {
                            if *debug {
                                jsn, _ := json.Marshal(nr)
                                log.Printf("[debug] %v", string(jsn))
                            }

                            if nr.Options.Status == "disabled" {
                                continue
                            }

                            if nr.Options.Command == "" {
                                nr.Options.Command = cn.Command
                            }

                            if nr.Options.Timeout == 0 {
                                nr.Options.Timeout = float64(cnTimeout / time.Second)
                            }

                            if nr.Options.MaxRespTime == 0 {
                                nr.Options.MaxRespTime = float64(cnMaxRespTime / time.Second)
                            }

                            wg.Add(1)

                            go func(nr cache.SockTable) {
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
                                                go runTrace(nr.Options.Command, tags, config)
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
                                                    go runTrace(nr.Options.Command, tags, config)
                                                }
                                            }
                                            
                                        }

                                    default:
                                        return
                                }

                                if result == 0 && response < nr.Options.MaxRespTime {
                                    trace = 0
                                }

                                if nr.Relation.Result != result || nr.Relation.Trace != trace {
                                    nr.Relation.Result = result
                                    nr.Relation.Trace = trace
                                    nrr.Data = append(nrr.Data, nr)
                                }

                                if nr.Relation.Response != response {
                                    nr.Relation.Response = response
                                }

                                // Adding metrics
                                if nr.Options.Service == "" {
                                    nr.Options.Service = "unknown"
                                }
                                if *plugin == "telegraf" {
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
                                if err = httpClient.WriteRecords(config, "/api/v1/netmap/status", jsn); err != nil {
                                    log.Printf("[error] %v", err)
                                }
                            }
                        }
                    }
                }

                time.Sleep(cnInterval)
            }
        }(cn)
    }

    // Netstat run cmd
    go func(){

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

        config := client.HttpConfig{
            URLs:            randURLs(cfg.Netstat.URLs),
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
            options := cache.Options {
                Status:      cfg.Netstat.Status,
                Timeout:     float64(netstatTimeout / time.Second),
                MaxRespTime: float64(netstatMaxRespTime / time.Second),
            }

            nrs, err := netstat.GetSocks(cfg.Netstat.IgnoreHosts, options)
            if err != nil {
                log.Printf("[error] %v", err)
            } else {
                if len(nrs.Data) > 0 {
                    jsn, err := json.Marshal(nrs)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        if err = httpClient.WriteRecords(config, "/api/v1/netmap/netstat", jsn); err != nil {
                            log.Printf("[error] %v", err)
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

