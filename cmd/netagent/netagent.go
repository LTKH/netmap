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
    "github.com/ltkh/netmap/internal/api/v1"
)

var (
    cacheConnections *Connections = NewCacheConnections()
)

type Connections struct {
    sync.RWMutex
    items          map[string]v1.SockTable
}

type Connection struct {
    URLs           []string                `toml:"urls"`
    Username       string                  `toml:"username"`
    Password       string                  `toml:"password"`
    BearerToken    string                  `toml:"bearer_token"`
    Headers        map[string]string       `toml:"headers"`
    TracerouteCmd  string                  `toml:"traceroute_cmd"`
    MaxRespTime    int                     `toml:"max_resp_time"`
    Timeout        time.Duration           `json:"timeout"`
    ReadTimeout    time.Duration           `json:"read_timeout"`
    Interval       time.Duration           `toml:"interval"`
}

type Config struct {
    Global         Global
    Connections    []Connection
}

type Global struct {
    URLs           []string                `toml:"urls"`
    NetstatMod     bool                    `toml:"netstat_mod"`
    IgnorePorts    []uint16                `toml:"ignore_ports"`
    Interval       time.Duration           `toml:"interval"`
}

// NetResponse struct
type NetResponse struct {
    Address        string                  `json:"address"`
    Timeout        time.Duration           `json:"timeout"`
    ReadTimeout    time.Duration           `json:"read_timeout"`
    Protocol       string                  `json:"protocol"`
}

type DataSend struct {
    Tags           map[string]interface{}  `json:"tags"`
    Fields         state.State             `json:"fields"`
    Output         []string                `json:"output"`
}

func NewCacheConnections() *Connections {
    cache := Connections{
        items: make(map[string]v1.SockTable),
    }
    return &cache
}

func (t *Connections) Set(key string, val v1.SockTable) {
    t.Lock()
    defer t.Unlock()
    t.items[key] = val
}

func (t *Connections) Get(key string) (v1.SockTable, bool) {
    t.RLock()
    defer t.RUnlock()
    val, found := t.items[key]
    if !found {
        return 0, false
    }
    return val, true
}


// TCPGather will execute if there are TCP tests defined in the configuration.
// It will return a map[string]interface{} for fields and a map[string]string for tags
func (n *NetResponse) TCPGather() (int, float64) {
    // Set default values
    if n.Timeout == 0 {
        n.Timeout = 5 * time.Second
    }
    if n.ReadTimeout == 0 {
        n.ReadTimeout = 5 * time.Second
    }
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
    cfFile          := flag.String("config", "netmap.toml", "config file")
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
    var cfg Config
    if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
        log.Fatalf("[error] %v", err)
    }
    f.Close()

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

    // Netstat run cmd
    if cfg.Global.NetstatMod == true {

        if cfg.Global.Interval == 0 {
            cfg.Global.Interval = 300
        }

        go func(){

            for {

                nd, err := netstat.GetSocks(cfg.Global.IgnorePorts)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    if len(nd.Data) > 0 {
                        jsn, err := json.Marshal(nd)
                        if err != nil {
                            log.Printf("[error] %v", err)
                        } else {
                            conn := client.New(client.HTTP{
                                URLs: cfg.Global.URLs,
                            })
                            _, err = conn.HttpRequest("POST", "/api/v1/netmap/netstat", jsn)
                            if err != nil {
                                log.Printf("[error] %v", err)
                            }
                        }
                    }
                }

                time.Sleep(time.Duration(cfg.Global.Interval) * time.Second)
            }
        }()
    }

    for _, cn := range cfg.Connections {

        go func(cn Connection) {

            if cn.Interval == 0 {
                cn.Interval = 60
            }

            for {

                hname, err := netstat.Hostname()
                if err != nil {
                    log.Printf("[error] %v", err)
                    time.Sleep(600 * time.Second)
                    continue
                }

                var nrs v1.NetstatData
                var nrr v1.NetstatData
                var data []v1.Alert
                var wg sync.WaitGroup

                //new client
                conn := client.New(client.HTTP{
                    URLs:        cn.URLs,
                    Username:    cn.Username,
                    Password:    cn.Password,
                    BearerToken: cn.BearerToken,
                    Headers:     cn.Headers,
                })

                //getting connections
                body, err := conn.HttpRequest("GET", "/api/v1/netmap/records?src_name="+hname, []byte(""))
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    if err := json.Unmarshal(body, &nrs); err != nil {
                        log.Printf("[error] error reading json from response body: %s", err)
                    }
                }

                for _, nr := range nrs.Data {
            
                    wg.Add(1)
                
                    go func(e v1.SockTable) {
                        defer wg.Done()

                        id := fmt.Sprintf("%v:%v:%v", e.LocalAddr.Name, e.Relation.Port, e.RemoteAddr.Name)
                        _, ok := cacheConnections.Get(id)
                        if ok == false {
                            cacheConnections.Set(id, e)
                        }
        
                        // Gather data
                        if e.Relation.Mode == "tcp" {
        
                            tcp := &NetResponse{
                                Address:         fmt.Sprintf("%v:%v", e.RemoteAddr.IP.String(), e.Relation.Port),
                                Timeout:         cn.Timeout,
                                ReadTimeout:     cn.ReadTimeout,
                                Protocol:        e.Relation.Mode,
                            }
        
                            result, response := tcp.TCPGather()
        
                            tags := map[string]string{
                                "src_name":   e.LocalAddr.Name,
                                "src_ip":     e.LocalAddr.IP.String(),
                                "dst_name":   e.RemoteAddr.Name,
                                "dst_ip":     e.RemoteAddr.IP.String(),
                                "port":       fmt.Sprintf("%v", e.Relation.Port),
                                "mode":       e.Relation.Mode,
                            }

                            if cn.MaxRespTime == 0 {
                                cn.MaxRespTime = 10
                            }
        
                            if (result == 1 || response > float64(cn.MaxRespTime)) {
                                if e.Relation.Trace == 0 {
                                    e.Relation.Trace = 1
        
                                    go func(address string, tags map[string]string){
        
                                        tmpl, err := newTemplate(address, cn.TracerouteCmd, tags)
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
        
                                        _, err = conn.HttpRequest("POST", "/api/v1/netmap/webhook", jsn)
                                        if err != nil {
                                            log.Printf("[error] %v", err)
                                        }

                                    }(tcp.Address, tags)
                                }
                            } else {
                                e.Relation.Trace = 0
                            }
        
                            if e.Relation.Result != result || result != 0 {
                                var alert v1.Alert
                                alert.Labels = tags
                                alert.Labels["alertname"] = "netmapResponseStatus"
                                if alert.Status = "resolved"; result != 0 {
                                    alert.Status = "firing"
                                }
                                data = append(data, alert)
                            }
        
                            e.Relation.Result = result
                            e.Relation.Response = response

                            nrr.Data = append(nrr.Data, e)
        
                            // Adding metrics
                            if *plugin == "telegraf" {
                                fmt.Printf(
                                    "netmap,src_name=%s,dst_name=%s,port=%d,mode=%s result_code=%d,response_time=%f\n", 
                                    e.LocalAddr.Name,
                                    e.RemoteAddr.Name,
                                    e.Relation.Port,
                                    e.Relation.Mode,
                                    e.Relation.Result,
                                    e.Relation.Response,
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
                        //write status to DB
                        _, err = conn.HttpRequest("POST", "/api/v1/netmap/status", jsn)
                        if err != nil {
                            log.Printf("[error] %v", err)
                        }
                    }
                }
        
                if len(data) > 0 {
        
                    //create json
                    jsn, err := json.Marshal(data)
                    if err != nil {
                        log.Printf("[error] %v", err)
                    } else {
                        //sending status
                        _, err = conn.HttpRequest("POST", "/api/v1/netmap/webhook", jsn)
                        if err != nil {
                            log.Printf("[error] %v", err)
                        }
                    }
                }

                time.Sleep(time.Duration(cn.Interval) * time.Second)
            }

        }(cn)        
        
    }

    // Daemon mode
    for (run) {

        if *plugin == "telegraf" {
            run = false
        }

        time.Sleep(time.Duration(*interval) * time.Second)

    }

}

