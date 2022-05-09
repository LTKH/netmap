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
}

type Cache struct {
    Limit          int                     `toml:"limit"`
}

type Netstat struct {
    Enabled        bool                    `toml:"enabled"`
    Send           bool                    `toml:"send"`
    Status         string                  `toml:"status"`
    IgnorePorts    []uint16                `toml:"ignore_ports"`
    Interval       time.Duration           `toml:"interval"`
    Command        string                  `toml:"command"`
    Timeout        time.Duration           `toml:"timeout"`
    MaxRespTime    float64                 `toml:"max_resp_time"`
}

type Connection struct {
    URLs           []string                `toml:"urls"`
    Username       string                  `toml:"username"`
    Password       string                  `toml:"password"`
    BearerToken    string                  `toml:"bearer_token"`
    Headers        map[string]string       `toml:"headers"`
    Command        string                  `toml:"command"`
    Interval       time.Duration           `toml:"interval"`
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

// TCPGather will execute if there are TCP tests defined in the configuration.
// It will return a map[string]interface{} for fields and a map[string]string for tags
func (n *NetResponse) TCPGather() (int, float64) {
    // Set default values
    if n.Timeout == 0 {
        n.Timeout = 5 
    }
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout("tcp", n.Address, n.Timeout * time.Second)
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

func runCommand(scmd string, timeout time.Duration) ([]byte, error) {
    log.Printf("[info] running '%s'", scmd)
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

    // Check the context error to see if the timeout was executed
    if ctx.Err() == context.DeadlineExceeded {
        return nil, fmt.Errorf("command timed out '%s'", scmd)
    }

    // If there's no context error, we know the command completed (or errored).
    if err != nil {
        return nil, fmt.Errorf("non-zero exit code: %v '%s'", err, scmd)
    }

    log.Printf("[info] finished '%s'", scmd)
    return out, nil
}

func newTemplate(def string, tstr string, vars interface{})(bytes.Buffer, error){

    funcMap := template.FuncMap{
        "hostname":        os.Hostname,
    }

    var tpl bytes.Buffer

    tmpl, err := template.New(def).Funcs(funcMap).Parse(tstr)
    if err != nil {
        return tpl, err
    }

    if err = tmpl.Execute(&tpl, &vars); err != nil {
        return tpl, err
    }

    return tpl, nil
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

    if cfg.Cache.Limit == 0 {
        cfg.Cache.Limit = 1000
    }

    cacheRecords := cache.NewCacheRecords(cfg.Cache.Limit)
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

        if cn.Interval == 0 {
            cn.Interval = 60
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
                                    if nr.Options.Command == "" {
                                        nr.Options.Command = cn.Command
                                    }
                                    if ok := cacheRecords.Set(id, nr); !ok {
                                        log.Printf("[error] %v - cache limit exceeded", id)
                                    }
                                }
                            }
                        }
                    }
                }

                time.Sleep(time.Duration(cn.Interval) * time.Second)
            }
        }(cn)
    }

    // Netstat run cmd
    go func(){

        if cfg.Netstat.Enabled == false {
            return
        }

        if cfg.Netstat.Interval == 0 {
            cfg.Netstat.Interval = 300
        }

        for {
            options := cache.Options {
                Status:      cfg.Netstat.Status,
                Command:     cfg.Netstat.Command,
                Timeout:     cfg.Netstat.Timeout,
                MaxRespTime: cfg.Netstat.MaxRespTime,
                ExpireTime:  time.Now().UTC().Unix() + int64(cfg.Netstat.Interval * 5),
            }
            nrs, err := netstat.GetSocks(cfg.Netstat.IgnorePorts, options)
            if err != nil {
                log.Printf("[error] %v", err)
            } else {
                var nrr v1.NetstatData

                for _, nr := range nrs.Data {
                    id := fmt.Sprintf("%v:%v:%v:%v", nr.LocalAddr.IP, nr.RemoteAddr.IP, nr.Relation.Mode, nr.Relation.Port)
                    _, ok := cacheRecords.Get(id)
                    if !ok {
                        nrr.Data = append(nrr.Data, nr)
                        if ok := cacheRecords.Set(id, nr); !ok {
                            log.Printf("[error] %v - cache limit exceeded", id)
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

            time.Sleep(time.Duration(cfg.Netstat.Interval) * time.Second)
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

                    // Gather data
                    if nr.Relation.Mode == "tcp" {
    
                        tcp := &NetResponse{
                            Address:         fmt.Sprintf("%v:%v", nr.RemoteAddr.IP.String(), nr.Relation.Port),
                            Timeout:         nr.Options.Timeout,
                            Protocol:        nr.Relation.Mode,
                        }
    
                        result, response := tcp.TCPGather()

                        if nr.Options.MaxRespTime == 0 {
                            nr.Options.MaxRespTime = 10
                        }
    
                        if (result == 1 || response > nr.Options.MaxRespTime) {
                            if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                nr.Relation.Trace = 1
    
                                go func(address string){

                                    tags := map[string]string{
                                        "src_name":   nr.LocalAddr.Name,
                                        "src_ip":     nr.LocalAddr.IP.String(),
                                        "dst_name":   nr.RemoteAddr.Name,
                                        "dst_ip":     nr.RemoteAddr.IP.String(),
                                        "port":       fmt.Sprintf("%v", nr.Relation.Port),
                                        "mode":       nr.Relation.Mode,
                                    }
    
                                    tmpl, err := newTemplate(address, nr.Options.Command, tags)
                                    if err != nil {
                                        log.Printf("[error] %v", err)
                                        return
                                    }
    
                                    out, err := runCommand(tmpl.String(), 600)
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

                                }(tcp.Address)
                            }
                        } else {
                            nr.Relation.Trace = 0
                        }

                        nr.Relation.Response = response
    
                        if nr.Relation.Result != result {
                            nr.Relation.Result = result
                            nrr.Data = append(nrr.Data, nr)
                        }

                        id := fmt.Sprintf("%v:%v:%v:%v", nr.LocalAddr.IP, nr.RemoteAddr.IP, nr.Relation.Mode, nr.Relation.Port)
                        if ok := cacheRecords.Set(id, nr); !ok {
                            log.Printf("[error] %v - cache limit exceeded", id)
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

            time.Sleep(time.Duration(60) * time.Second)
        }
    }()

    // Daemon mode
    for (run) {

        if *plugin == "telegraf" {
            //run = false
        }

        time.Sleep(time.Duration(*interval) * time.Second)
    }

}

