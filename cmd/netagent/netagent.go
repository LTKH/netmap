package main

import (
    "log"
    "time"
    "os"
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
    "github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/state"
    "github.com/ltkh/netmap/internal/netstat"
    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/api/v1"
)

type Config struct {
    Global         Global                  `toml:"global"`
    Cache          *Cache                  `toml:"cache"`
    Netstat        *Netstat                `toml:"netstat"`
    Connections    []Connection            `toml:"connections"`
}

type Global struct {
    URLs           []string                `toml:"urls"`
    Interval       string                  `toml:"interval"`
    MaxRespTime    string                  `toml:"max_resp_time"`
}

type Cache struct {
    Limit          int                     `toml:"limit"`
    FlushInterval  string                  `toml:"flush_interval"`
}

type Netstat struct {
    Enabled        bool                    `toml:"enabled"`
    Send           bool                    `toml:"send"`
    Status         string                  `toml:"status"`
    IgnorePorts    []uint16                `toml:"ignore_ports"`
    Interval       string                  `toml:"interval"`
    Command        string                  `toml:"command"`
    Timeout        string                  `toml:"timeout"`
}

type Connection struct {
    URLs           []string                `toml:"urls"`
    Username       string                  `toml:"username"`
    Password       string                  `toml:"password"`
    BearerToken    string                  `toml:"bearer_token"`
    Headers        map[string]string       `toml:"headers"`
    Command        string                  `toml:"command"`
    Interval       string                  `toml:"interval"`
}

// NetResponse struct
type NetResponse struct {
    Address        string                  `json:"address"`
    Timeout        time.Duration           `json:"timeout"`
    Protocol       string                  `json:"protocol"`
}

type DataSend struct {
    Tags           map[string]interface{}  `json:"tags"`
    Fields         state.State             `json:"fields"`
    Output         []string                `json:"output"`
}

func (n *NetResponse) DialTimeout(network string) (int, float64) {
    // Set default values
    if n.Timeout == 0 {
        n.Timeout = 5 
    }
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout(network, n.Address, n.Timeout)
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

func runTrace(cmd string, tags map[string]string, conn client.HTTP) {

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

    var dt []v1.Alert
    var al v1.Alert

    al.Labels = tags
    al.Labels["alertname"] = "netmapTraceroute"
    al.Annotations.Description = string(out)

    dt = append(dt, al)

    jsn, err := json.Marshal(dt)
    if err != nil {
        log.Printf("[error] %v", err)
        return
    }

    _, _, err = conn.HttpRequest("POST", "/api/v1/netmap/webhook", jsn)
    if err != nil {
        log.Printf("[error] %v", err)
    }

    return
}

func main() {

    // Limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    // Command-line flag parsing
    cfFile          := flag.String("config", "config/netmap.toml", "config file")
    lgFile          := flag.String("logfile", "", "log file")
    interval        := flag.Int("interval", 30, "interval")
    plugin          := flag.String("plugin", "", "plugin")
    log_max_size    := flag.Int("log_max_size", 1, "log max size") 
    log_max_backups := flag.Int("log_max_backups", 3, "log max backups")
    log_max_age     := flag.Int("log_max_age", 10, "log max age")
    log_compress    := flag.Bool("log_compress", true, "log compress")
    debug           := flag.Bool("debug", false, "debug mode")
    flag.Parse()

    // Logging settings
    if *lgFile != "" || *plugin != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *log_max_size,    // megabytes after which new file is created
            MaxBackups: *log_max_backups, // number of backups
            MaxAge:     *log_max_age,     // days
            Compress:   *log_compress,    // using gzip
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

    log.Print("[info] netmap started -_-")

    //Set CacheLimit
    if cfg.Cache.Limit == 0 {
        cfg.Cache.Limit = 1000
    }

    // Set CacheFlushInterval
    if cfg.Cache.FlushInterval == "" {
        cfg.Cache.FlushInterval = "24h"
    }
    flushInterval, _ := time.ParseDuration(cfg.Cache.FlushInterval)
    if flushInterval == 0 {
        log.Fatal("[error] setting cache flush interval: invalid duration")
    }

    cacheRecords := cache.NewCacheRecords(cfg.Cache.Limit, flushInterval)
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

    // Set Global Interval
    if cfg.Global.Interval == "" {
        cfg.Global.Interval = "60s"
    }
    globalInterval, _ := time.ParseDuration(cfg.Global.Interval)
    if globalInterval == 0 {
        log.Fatal("[error] setting global interval: invalid duration")
    }

    // Get connections
    for _, cn := range cfg.Connections {

        // Set Interval
        if cn.Interval == "" {
            cn.Interval = cfg.Global.Interval
        }
        cnInterval, _ := time.ParseDuration(cn.Interval)
        if cnInterval == 0 {
            log.Fatal("[error] setting connection interval: invalid duration")
        }

        go func(cn Connection) {

            for {
                hname, err := netstat.Hostname()
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    //new client
                    conn := client.New(client.HTTP{
                        URLs:        cn.URLs,
                        Username:    cn.Username,
                        Password:    cn.Password,
                        BearerToken: cn.BearerToken,
                        Headers:     cn.Headers,
                    })

                    var nrs v1.NetstatData
                    stat := cacheRecords.GetStatistics()
                    body, code, err := conn.HttpRequest("GET", fmt.Sprintf("/api/v1/netmap/records?src_name=%s&total=%d&disabled=%d", hname, stat.Total, stat.Disabled), []byte(""))
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        if code == 200 {
                            if err := json.Unmarshal(body, &nrs); err != nil {
                                log.Printf("[error] %v", err)
                            } else {
                                for _, nr := range nrs.Data {
                                    id := fmt.Sprintf("%v:%v:%v:%v", nr.LocalAddr.IP, nr.RemoteAddr.IP, nr.Relation.Mode, nr.Relation.Port)
                                    nr.Options.ExpireTime = 0
                                    if nr.Options.Command == "" {
                                        nr.Options.Command = cn.Command
                                    }
                                    if err := cacheRecords.Set(id, nr); err != nil {
                                        log.Printf("[error] %v", err)
                                    }
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

        if cfg.Netstat.Enabled == false {
            return
        }

        // Set Timeout
        if cfg.Netstat.Timeout == "" {
            cfg.Netstat.Timeout = "10s"
        }
        netstatTimeout, _ := time.ParseDuration(cfg.Netstat.Timeout)
        if netstatTimeout == 0 {
            log.Fatal("[error] setting netstat timeout: invalid duration")
        }

        // Set Interval
        if cfg.Netstat.Interval == "" {
            cfg.Netstat.Interval = "300s"
        }
        netstatInterval, _ := time.ParseDuration(cfg.Netstat.Interval)
        if netstatInterval == 0 {
            log.Fatal("[error] setting netstat interval: invalid duration")
        }

        // Set MaxRespTime
        if cfg.Global.MaxRespTime == "" {
            cfg.Global.MaxRespTime = "10s"
        }
        maxRespTime, _ := time.ParseDuration(cfg.Global.MaxRespTime)
        if maxRespTime == 0 {
            log.Fatal("[error] setting global max_resp_time: invalid duration")
        }

        for {
            options := cache.Options {
                Status:      cfg.Netstat.Status,
                Command:     cfg.Netstat.Command,
                Timeout:     netstatTimeout.Seconds(),
                MaxRespTime: maxRespTime.Seconds(),
            }

            nrs, err := netstat.GetSocks(cfg.Netstat.IgnorePorts, options)
            if err != nil {
                log.Printf("[error] %v", err)
            } else {
                var nrr v1.NetstatData

                for _, nr := range nrs.Data {
                    id := fmt.Sprintf("%v:%v:%v:%v", nr.LocalAddr.IP, nr.RemoteAddr.IP, nr.Relation.Mode, nr.Relation.Port)
                    val, ok := cacheRecords.Get(id)
                    if !ok {
                        nrr.Data = append(nrr.Data, nr)
                        if err := cacheRecords.Set(id, nr); err != nil {
                            log.Printf("[error] %v", err)
                        }
                    } else {
                        val.Options.ExpireTime = 0
                        if err := cacheRecords.Set(id, val); err != nil {
                            log.Printf("[error] %v", err)
                        }
                    }
                }

                if cfg.Netstat.Send && len(nrr.Data) > 0 {
                    jsn, err := json.Marshal(nrr)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        conn := client.New(client.HTTP{
                            URLs: cfg.Global.URLs,
                        })
                        _, _, err = conn.HttpRequest("POST", "/api/v1/netmap/netstat", jsn)
                        if err != nil {
                            log.Printf("[error] %v", err)
                        }
                    }
                }
            }

            time.Sleep(netstatInterval)
        }
    }()

    // Check connections
    go func() {

        for {

            // Deleting expired records
            cacheRecords.DelExpiredItems()

            var wg sync.WaitGroup
            var nrr v1.NetstatData

            conn := client.New(client.HTTP{
                URLs: cfg.Global.URLs,
            })

            stat := cache.Statistics{}
            
            for _, nr := range cacheRecords.Items() {

                stat.Total = stat.Total +1

                if *debug {
                    jsn, _ := json.Marshal(nr)
                    log.Printf("[debug] %v", string(jsn))
                }

                if nr.Options.Status != "" {
                    stat.Disabled = stat.Disabled +1
                    continue
                }

                wg.Add(1)

                go func(nr cache.SockTable) {
                    defer wg.Done()

                    result := 0
                    response := float64(0)
                    tags := map[string]string{
                        "src_name":   nr.LocalAddr.Name,
                        "src_ip":     nr.LocalAddr.IP.String(),
                        "dst_name":   nr.RemoteAddr.Name,
                        "dst_ip":     nr.RemoteAddr.IP.String(),
                        "port":       fmt.Sprintf("%v", nr.Relation.Port),
                        "mode":       nr.Relation.Mode,
                    }

                    switch nr.Relation.Mode {

                        case "tcp","udp":
                            net := &NetResponse{
                                Address:         fmt.Sprintf("%v:%v", nr.RemoteAddr.IP.String(), nr.Relation.Port),
                                Timeout:         time.Duration(nr.Options.Timeout) * time.Second,
                                Protocol:        nr.Relation.Mode,
                            }
        
                            result, response = net.DialTimeout(nr.Relation.Mode)
        
                            if (result == 1 || response > nr.Options.MaxRespTime) {
                                if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                    nr.Relation.Trace = 1
                                    go runTrace(nr.Options.Command, tags, conn)
                                }
                            } else {
                                nr.Relation.Trace = 0
                            }

                        case "cmd":
                            cmd := newTemplate(nr.Relation.Command, tags)

                            if cmd != "" {
                                _, response, err = runCommand(cmd, time.Duration(nr.Options.Timeout) * time.Second)
                                if err != nil {
                                    result = 1
                                    if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                        nr.Relation.Trace = 1
                                        go runTrace(nr.Options.Command, tags, conn)
                                    }
                                } else {
                                    nr.Relation.Trace = 0
                                }
                            }
                        default:
                            return
                    }

                    if nr.Relation.Result != result {
                        nr.Relation.Result = result
                        nrr.Data = append(nrr.Data, nr)
                    }

                    if nr.Relation.Response != response {
                        nr.Relation.Response = response
                    }

                    id := fmt.Sprintf("%v:%v:%v:%v", nr.LocalAddr.IP, nr.RemoteAddr.IP, nr.Relation.Mode, nr.Relation.Port)
                    if err := cacheRecords.Set(id, nr); err != nil {
                        log.Printf("[error] %v", err)
                    }

                    // Adding metrics
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
            }

            wg.Wait()

            if len(nrr.Data) > 0 {
    
                //create json
                jsn, err := json.Marshal(nrr)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    //sending status
                    _, _, err = conn.HttpRequest("POST", "/api/v1/netmap/status", jsn)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    }
                }
            }

            if *plugin == "telegraf" {
                fmt.Printf(
                    "netmap conn_total=%d,conn_disabled=%d\n",
                    stat.Total,
                    stat.Disabled,
                )
            }

            time.Sleep(globalInterval)
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

