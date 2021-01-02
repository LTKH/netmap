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
    "strings"
    "os/exec"
    "sync"
    "context"
    "bytes"
    "encoding/json"
    "text/template"
    "github.com/naoina/toml"
    "gopkg.in/natefinch/lumberjack.v2"
    "github.com/ltkh/netmap/internal/http"
    "github.com/ltkh/netmap/internal/state"
)

type Connection struct {
    URLs           []string                `toml:"urls"`
    Username       string                  `toml:"username"`
    Password       string                  `toml:"password"`
    BearerToken    string                  `toml:"bearer_token"`
    Headers        map[string]string       `toml:"headers"`
    TracerouteCmd  string                  `toml:"traceroute_cmd"`
    MaxRespTime    float64                 `toml:"max_resp_time"`
}

type Config struct {
    Global         Global
    Connections    []Connection
}

type Global struct {
    URLs           []string                `toml:"urls"`
    NetstatCmd     string                  `toml:"netstat_cmd"`
    Interval       time.Duration           `json:"interval"`
}

// NetResponse struct
type NetResponse struct {
    Address        string                  `json:"address"`
    Timeout        time.Duration           `json:"timeout"`
    ReadTimeout    time.Duration           `json:"read_timeout"`
    Protocol       string                  `json:"protocol"`
}

type DataSend struct {
    Tags           map[string]string       `json:"tags"`
    Fields         state.State             `json:"fields"`
    Output         []string                `json:"output"`
}

// TCPGather will execute if there are TCP tests defined in the configuration.
// It will return a map[string]interface{} for fields and a map[string]string for tags
func (n *NetResponse) TCPGather() (int, float64) {
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout("tcp", n.Address, n.Timeout)
    // Stop timer
    responseTime := time.Since(start).Seconds()
    // Handle error
    if err != nil {
        log.Printf("[error] %v - %v", n.Address, err)

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

func newTemplate(def string, tstr string, tags interface{})(bytes.Buffer, error){

    var tpl bytes.Buffer

    tmpl, err := template.New(def).Parse(tstr)
    if err != nil {
        return tpl, err
    }

    if err = tmpl.Execute(&tpl, &tags); err != nil {
        return tpl, err
    }

    return tpl, nil
}

func main() {

    // Limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    // Command-line flag parsing
    cfFile          := flag.String("config", "", "config file")
    lgFile          := flag.String("logfile", "", "log file")
    interval        := flag.Int("interval", 30, "interval")
    plugin          := flag.String("plugin", "", "plugin")
    log_max_size    := flag.Int("log_max_size", 1, "log max size") 
    log_max_backups := flag.Int("log_max_backups", 3, "log max backups")
    log_max_age     := flag.Int("log_max_age", 10, "log max age")
    log_compress    := flag.Bool("log_compress", true, "log compress")
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
    defer f.Close()
    
    var cfg Config
    if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
        log.Fatalf("[error] %v", err)
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

    // Netmap run cmd
    if cfg.Global.Interval > 0 {
        go func(){
            for {
                out, err := runCommand(cfg.Global.NetstatCmd, 300)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    tags := map[string]string{"cmd": "netstat"}
                    fields := state.State{}
                    data := DataSend{ Tags: tags, Fields: fields, Output: make([]string, 0) }
                    data.Output = strings.Split(string(out), "\n")
                    jsn, err := json.Marshal(data)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        conn := http.New(http.HTTP{
                            URLs:        cfg.Global.URLs,
                        })
                        _, err = conn.GatherURL("POST", string(jsn))
                        if err != nil {
                            log.Printf("[error] %v", err)
                        }
                    }
                }
                time.Sleep(time.Duration(cfg.Global.Interval) * time.Second)
            }
        }()
    }
    
    // Сache initialization
    cache := state.NewCacheStates()

    // Daemon mode
    for (run) {

        if *plugin == "telegraf" {
            run = false
        }

        var wg sync.WaitGroup

        for _, cn := range cfg.Connections {

            var nrs []NetResponse

            conn := http.New(http.HTTP{
                URLs:        cn.URLs,
                Username:    cn.Username,
                Password:    cn.Password,
                BearerToken: cn.BearerToken,
                Headers:     cn.Headers,
            })

            body, err := conn.GatherURL("GET", "")
            if err != nil {
                log.Printf("[error] %v", err)
            } else {
                if err := json.Unmarshal(body, &nrs); err != nil {
                    log.Printf("[error] error reading json from response body: %s", err)
                }
            }          
            
            for _, nr := range nrs {
                wg.Add(1)
            
                go func(n NetResponse) {
                    defer wg.Done()
                    
                    // Set default values
                    if n.Timeout == 0 {
                        n.Timeout = 5 * time.Second
                    }
                    if n.ReadTimeout == 0 {
                        n.ReadTimeout = 5 * time.Second
                    }
                    if cn.MaxRespTime == 0 {
                        cn.MaxRespTime = 5
                    }

                    // Prepare host and port
                    host, port, err := net.SplitHostPort(n.Address)
                    if err != nil {
                        log.Printf("[error] %v", err)
                        return 
                    }
                    if host == "" {
                        log.Print("[error] bad host")
                        return
                    }
                    if port == "" {
                        log.Print("[error] bad port")
                        return
                    }
                    
                    // Prepare data
                    tags := map[string]string{"server": host, "port": port, "protocol": n.Protocol}
                    fields, ok := cache.Get(n.Protocol+"-"+n.Address)
                    if !ok {
                        cache.Set(n.Protocol+"-"+n.Address, fields)
                    }

                    // Gather data
                    if n.Protocol == "tcp" {

                        state := false
                        result, response := n.TCPGather()

                        if result != fields.ResultCode {
                            state = true
                        } 

                        fields.ResultCode = result
                        fields.ResponseTime = response
                        data := DataSend{ Tags: tags, Fields: fields, Output: make([]string, 0) }

                        if state {
                            jsn, err := json.Marshal(data)
                            if err != nil {
                                log.Printf("[error] %v", err)
                            } else {
                                _, err = conn.GatherURL("POST", string(jsn))
                                if err != nil {
                                    log.Printf("[error] %v", err)
                                }
                            }
                            //log.Printf("[info] %v", string(jsn))
                        }

                        if (result == 1 || response > cn.MaxRespTime) && fields.Traceroute == 0 {
                            fields.Traceroute = 1
                            go func(data DataSend){
                                tmpl, err := newTemplate(n.Address, cn.TracerouteCmd, tags)
                                if err != nil {
                                    log.Printf("[error] %v", err)
                                    return
                                }

                                out, err := runCommand(tmpl.String(), 300)
                                if err != nil {
                                    log.Printf("[error] %v", err)
                                    return
                                }

                                data.Output = strings.Split(string(out), "\n")
                                data.Fields.Traceroute = 1
                                jsn, err := json.Marshal(data)
                                if err != nil {
                                    log.Printf("[error] %v", err)
                                    return
                                }

                                _, err = conn.GatherURL("POST", string(jsn))
                                if err != nil {
                                    log.Printf("[error] %v", err)
                                }

                                //log.Printf("[info] %v", string(jsn))
                            }(data)
                        }

                        if result == 0 && response <= cn.MaxRespTime && fields.Traceroute == 1 {
                            fields.Traceroute = 0
                        }

                    //} else if n.Protocol == "udp" {
                        //result, response = n.UDPGather()
                    } else {
                        log.Print("[error] bad protocol")
                        return
                    }

                    // Adding metrics
                    if *plugin == "telegraf" {
                        fmt.Printf(
                            "netmap,server=%s,port=%s,protocol=%s result_code=%d,response_time=%f\n", 
                            host,
                            port,
                            n.Protocol,
                            fields.ResultCode,
                            fields.ResponseTime,
                        )
                    }

                    // Adding to cache
                    fields.EndsAt = time.Now().UTC().Unix() + 600
                    cache.Set(n.Protocol+"-"+n.Address, fields)

                    // Delete expired items
                    for _, v := range cache.DelExpiredItems() {
                        log.Printf("[info] deleted cache key :%s", v)
                    }

                }(nr)
            }

        }

        wg.Wait()
        
        time.Sleep(time.Duration(*interval) * time.Second)

    }

}

