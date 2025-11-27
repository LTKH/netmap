package main

import (
    "os"
    "os/exec"
    "os/signal"
    "fmt"
    "log"
    "net"
    "time"
    "flag"
    "sync"
    "bytes"
    "bufio"
    "context"
    "math/rand"
    "crypto/aes"
    "crypto/cipher"
    "runtime"
    "syscall"
    "strings"
    "text/template"
    "encoding/json"
    "encoding/base64"

    "github.com/ltkh/netmap/internal/cache"
    "github.com/ltkh/netmap/internal/client"
    "github.com/ltkh/netmap/internal/config"
    "github.com/ltkh/netmap/internal/netstat"
    "github.com/naoina/toml"
    "github.com/pkg/errors"
    "gopkg.in/natefinch/lumberjack.v2"
)

var (
    httpClient   = client.NewHttpClient(nil)
    cacheRecords = cache.NewCacheRecords(10000)
    Version      = "unknown"
    KeyString    = "khuyg743878g8s2:b970m-z0"
    statusChan   chan config.SockTable
    webhookChan  chan Output
)

type Config struct {
    Global          *Global                  `toml:"global"`
    Netstat         *Netstat                 `toml:"netstat"`
    Connections     *Connection              `toml:"connections"`
}

type Global struct {
    URLs            []string                 `toml:"urls"`
    ContentEncoding string                   `toml:"content_encoding"`
    Username         string                  `toml:"username"`
    Password         string                  `toml:"password"`
    Interval        string                   `toml:"interval"`
    Timeout         string                   `toml:"timeout"`
    MaxRespTime     string                   `toml:"max_resp_time"`
    AccountID       uint32                   `toml:"account_id"`
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
    Username         string                  `toml:"username"`
    Password         string                  `toml:"password"`
}

type Connection struct {
    URLs             []string                `toml:"urls"`
    Path             string                  `toml:"path"`
    ContentEncoding  string                  `toml:"content_encoding"`
    Command          string                  `toml:"command"`
    Interval         string                  `toml:"interval"`
    Timeout          string                  `toml:"timeout"`
    MaxRespTime      string                  `toml:"max_resp_time"`
    Username         string                  `toml:"username"`
    Password         string                  `toml:"password"`
}

type NetResponse struct {
    Address          string                  `json:"address"`
    Timeout          time.Duration           `json:"timeout"`
    Protocol         string                  `json:"protocol"`
}

type Output struct {
    Id               string
    Stdout           string
    Stderr           string
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

func encrypt(text string) (string, error) {
    block, err := aes.NewCipher([]byte(KeyString))
    if err != nil {
        return "", err
    }
    plainText := []byte(text)
    bytes := []byte{35, 46, 57, 24, 85, 35, 24, 74, 87, 35, 88, 98, 66, 32, 14, 05}
    cfb := cipher.NewCFBEncrypter(block, bytes)
    cipherText := make([]byte, len(plainText))
    cfb.XORKeyStream(cipherText, plainText)
    return base64.StdEncoding.EncodeToString(cipherText), nil
}
 
func decrypt(text string) (string, error) {
    block, err := aes.NewCipher([]byte(KeyString))
    if err != nil {
        return "", err
    }
    cipherText, err := base64.StdEncoding.DecodeString(text)
    if err != nil {
        return "", err
    }
    bytes := []byte{35, 46, 57, 24, 85, 35, 24, 74, 87, 35, 88, 98, 66, 32, 14, 05}
    cfb := cipher.NewCFBDecrypter(block, bytes)
    plainText := make([]byte, len(cipherText))
    cfb.XORKeyStream(plainText, cipherText)
    return string(plainText), nil
}

func getHostname() string {
    // Get hostname
    hname, err := netstat.Hostname()
    if err != nil {
        return "unknown"
    }
    return hname
}

func randURLs(urls []string) []string {
    rand.Seed(time.Now().UnixNano())
    rand.Shuffle(len(urls), func(i, j int) { urls[i], urls[j] = urls[j], urls[i] })
    return urls
}

func dialTimeout(network, address string, timeout time.Duration) (int32, float32) {
    // Set default values
    if timeout == 0 {
        timeout = 5
    }
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout(network, address, timeout)
    // Stop timer
    responseTime := float32(time.Since(start).Seconds())
    // Handle error
    if err != nil {
        log.Printf("[error] %v", err)

        if e, ok := err.(net.Error); ok && e.Timeout() {
            return int32(1), responseTime
        }
        return int32(2), responseTime
    }

    defer conn.Close()

    return int32(0), responseTime
}

func runCommand(scmd string, timeout time.Duration) ([]byte, float32, error) {
    log.Printf("[info] running '%s'", scmd)

    // Start Timer
    start := time.Now()

    // Create a new context and add a timeout to it
    ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
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
    responseTime := float32(time.Since(start).Seconds())

    // Check the context error to see if the timeout was executed
    if ctx.Err() == context.DeadlineExceeded {
        return nil, responseTime, fmt.Errorf("command timed out '%s'", scmd)
    }

    // If there's no context error, we know the command completed (or errored).
    if err != nil {
        return nil, responseTime, fmt.Errorf("non-zero exit code: %v '%s'", err, scmd)
    }

    return out, responseTime, nil
}

func runCmdWithOutput(id, scmd string, timeout time.Duration, debug bool) (int32, error) {
    if timeout == 0 { timeout = 3600 }
    
    if _, err := cacheRecords.GetState(id); err != nil {
        return 0, err
    }

    log.Printf("[info] running '%s'", scmd)

    // Create a new context and add a timeout to it
    ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
    defer cancel() // The cancel should be deferred so resources are cleaned up

    // Create the command with our context
    var cmd *exec.Cmd
    if runtime.GOOS == "windows" {
        cmd = exec.CommandContext(ctx, "cmd", "/C", scmd)
    } else {
        cmd = exec.CommandContext(ctx, "/bin/sh", "-c", scmd)
    }

    // Получаем stdout
    stdoutPipe, err := cmd.StdoutPipe()
    if err != nil {
        //fmt.Println("Error obtaining stdout:", err)
        return 0, err
    }

    // Получаем stderr
    stderrPipe, err := cmd.StderrPipe()
    if err != nil {
        fmt.Println("Error obtaining stderr:", err)
        return 0, err
    }

    var wg sync.WaitGroup
    wg.Add(2)
    var stderrBuffer bytes.Buffer // Буфер для сбора STDERR

    // Чтение stdout в реальном времени
    go func() {
        defer wg.Done()
        scanner := bufio.NewScanner(stdoutPipe)
        for scanner.Scan() {
            if _, err := cacheRecords.GetState(id); err != nil {
                return
            }

            if debug {
                log.Printf("[debug] stdout: %v", scanner.Text())
            }

            output := Output{
                Id: id,
                Stdout: scanner.Text(),
            }

            select {
            case webhookChan <- output:
                //log.Printf("[debug] len chan - %v", len(webhookChan))
            default: 
                // Канал переполнен, можно удалить или игнорировать
            }
        }
    }()

    // Чтение stderr в реальном времени
    go func() {
        defer wg.Done()
        scanner := bufio.NewScanner(stderrPipe)
        for scanner.Scan() {
            line := scanner.Text()
            stderrBuffer.WriteString(line + "\n")
        }
    }()

    // Запускаем команду
    if err := cmd.Start(); err != nil {
        wg.Wait()
        return 0, err
    }

    // Ждем завершения команды
    err = cmd.Wait()
    
    // Ждем завершения чтения обоих пайпов
    wg.Wait()

    if stderrBuffer.Len() > 0 {
        return 1, fmt.Errorf("%v", stderrBuffer.String())
    }
    
    // Ждем завершения команды
    if err != nil {
        if ctx.Err() == context.DeadlineExceeded {
            return 2, nil
        }
        return 1, err
    }

    return 0, nil
}

func newTemplate(cmd string, tags map[string]string) string {

    var tpl bytes.Buffer

    funcMap := template.FuncMap{
        "hostname": os.Hostname,
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

func runTrace(id, cmd, name string, tags map[string]string, cfg client.HttpConfig) {
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

    output := Output{
        Id: id,
        Stdout: string(out),
    }

    select {
    case webhookChan <- output:
        //log.Printf("[debug] len chan - %v", len(api.Collect))
    default: 
        // Канал переполнен, можно удалить или игнорировать
    }

    return
}

// Get connections
func getConnections(clnt client.HttpConfig, cfg Config, hname string, debug bool) {

    if len(cfg.Connections.URLs) == 0 {
        return
    }
    if cfg.Connections.Path == "" {
        cfg.Connections.Path = fmt.Sprintf("/api/v1/netmap/records?src_name=%s", hname)
    } else {
        tags := map[string]string{"hostname": getHostname()}
        cfg.Connections.Path = newTemplate(cfg.Connections.Path, tags)
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

    body, err := httpClient.ReadRecords(clnt, cfg.Connections.Path)
    if err != nil {
        log.Printf("[error] %v - %s", err, cfg.Connections.Path)
        return
    }

    var nrs netstat.NetstatData
    err = json.Unmarshal(body, &nrs)
    if err != nil {
        log.Printf("[error] %v - %s", err, cfg.Connections.Path)
        return
    }

    if debug {
        log.Printf("[debug] GET - %s (%v)", cfg.Connections.Path, len(nrs.Data))
    }

    records := make(map[string]config.SockTable)
    for _, nr := range nrs.Data {
        if nr.Options.Command == "" {
            nr.Options.Command = cfg.Connections.Command
        }

        if nr.Relation.Mode != "cmd" && nr.Options.Timeout == 0 {
            nr.Options.Timeout = float32(cnTimeout / time.Second)
        }

        if nr.Relation.Mode != "cmd" && nr.Options.MaxRespTime == 0 {
            nr.Options.MaxRespTime = float32(cnMaxRespTime / time.Second)
        }

        id := config.GetIdRec(&nr)
        records[id] = nr

        err := cacheRecords.Set(id, nr)
        if err != nil {
            log.Printf("[error] %v", err)
        }

        if debug {
            log.Printf(
                "[debug] cache: set record name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f",
                nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response,
            )
        }
    }

    // Removing missing entries from the cache
    items := cacheRecords.Items()
    for _, nr := range items {
        if _, found := records[nr.Id]; !found {
            cacheRecords.Del(nr.Id)
            if debug {
                log.Printf(
                    "[debug] cache: del record name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f",
                    nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response,
                )
            }
        }
    }
}

// loading configuration file
func loadConfigFile(file string, dcrpt bool) (Config, error) {
    var cfg Config

    // Loading configuration file
    f, err := os.Open(file)
    if err != nil {
        return cfg, err
    }
    
    if err := toml.NewDecoder(f).Decode(&cfg); err != nil {
        return cfg, err
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
 
    // Decrypt password
    if dcrpt {
        if cfg.Global.Password != "" {
            passwd, err := decrypt(cfg.Global.Password)
            if err != nil {
                return cfg, err
            }
            cfg.Global.Password = passwd
        }
        if cfg.Netstat.Password != "" {
            passwd, err := decrypt(cfg.Netstat.Password)
            if err != nil {
                return cfg, err
            }
            cfg.Netstat.Password = passwd
        }
        if cfg.Connections.Password != "" {
            passwd, err := decrypt(cfg.Connections.Password)
            if err != nil {
                return cfg, err
            }
            cfg.Connections.Password = passwd
        }
    }

    return cfg, nil
}

func sendStatus(clnt client.HttpConfig, cfg Config, debug bool) {
    nrr := netstat.NetstatData{}
    timeout := 15 * time.Second
    timer := time.NewTimer(timeout)

    for {
        select {
        case item, _ := <-statusChan:
            // Добавляем элемент в текущую пачку
            nrr.Data = append(nrr.Data, item)
        case <-timer.C:
            // Сработал таймаут: отправляем пачку
            if len(nrr.Data) > 0 {
                // Create json
                jsn, err := json.Marshal(nrr)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    if debug {
                        log.Printf("[debug] POST - /api/v1/netmap/status (%v)", len(nrr.Data))
                        for _, nr := range nrr.Data {
                            log.Printf(
                                "[debug] server: send status record name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f",
                                nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response,
                            )
                        }
                    }
                    // Sending status
                    if err = httpClient.WriteRecords(clnt, "/api/v1/netmap/status", jsn); err != nil {
                        log.Printf("[error] %v - /api/v1/netmap/status", err)
                    }
                }

                nrr = netstat.NetstatData{} // Сбрасываем пачку
            }
            timer.Reset(timeout) // Сбрасываем таймер для следующего интервала
        }
    }
}

func sendWebhook(clnt client.HttpConfig, cfg Config, debug bool) {
    mlt := map[string]Alert{}
    timeout := 15 * time.Second
    timer := time.NewTimer(timeout)
    
    for {
        select {
        case item, _ := <-webhookChan:
            // Добавляем элемент в текущую пачку
            alt, ok := mlt[item.Id]
            if !ok {
                alt = Alert{}

                rec, ok := cacheRecords.Get(item.Id)
                if !ok { continue }

                alt.Labels = map[string]string{
                    "src_name":   rec.LocalAddr.Name,
                    "src_ip":     rec.LocalAddr.IP,
                    "dst_name":   rec.RemoteAddr.Name,
                    "dst_ip":     rec.RemoteAddr.IP,
                    "port":       fmt.Sprintf("%v", rec.Relation.Port),
                    "mode":       rec.Relation.Mode,
                    "service":    rec.Options.Service,
                    "status":     rec.Options.Status,
                    "account_id": fmt.Sprintf("%v", rec.Options.AccountID),
                }

                alt.Annotations.Description = item.Stdout
            } else {
                alt.Annotations.Description = strings.Join([]string{alt.Annotations.Description, item.Stdout}, "\n")
            }
            
            mlt[item.Id] = alt
        case <-timer.C:
            // Сработал таймаут: отправляем пачку
            if len(mlt) > 0 {
                var dtt []Alert

                for _, ml := range mlt {
                    dtt = append(dtt, ml)
                }

                jsn, err := json.Marshal(dtt)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    if debug {
                        log.Printf("[debug] POST - /api/v1/netmap/webhook (%v)", len(dtt))
                        for _, dt := range dtt {
                            log.Printf(
                                "[debug] server: send webhook record name=%s,ip=%s,mode=%s,lines=%d",
                                dt.Labels["dst_name"], dt.Labels["dst_ip"], dt.Labels["mode"], len(strings.Split(dt.Annotations.Description, "\n")),
                            )
                        }
                    }
                    // Sending webhook
                    if err := httpClient.WriteRecords(clnt, "/api/v1/netmap/webhook", jsn); err != nil {
                        log.Printf("[error] %v - /api/v1/netmap/webhook", err)
                    }
                }
                
                mlt = map[string]Alert{} // Сбрасываем пачку
            }
            timer.Reset(timeout) // Сбрасываем таймер для следующего интервала
        }
    }
}

func main() {

    // Limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    // Command-line flag parsing
    cfFile          := flag.String("config.file", "config/netmap.toml", "config file")
    interval        := flag.Int("interval", 30, "interval")
    plugin          := flag.String("plugin", "", "plugin")
    lgFile          := flag.String("log.file", "", "log file")
    logMaxSize      := flag.Int("log.max-size", 1, "log max size")
    logMaxBackups   := flag.Int("log.max-backups", 3, "log max backups")
    logMaxAge       := flag.Int("log.max-age", 10, "log max age")
    logCompress     := flag.Bool("log.compress", true, "log compress")
    version         := flag.Bool("version", false, "show netagent version")
    debug           := flag.Bool("debug", false, "debug mode")
    encryptPass     := flag.String("encrypt", "", "encrypt string")
    decryptPass     := flag.Bool("decrypt", false, "decrypt password string")

    flag.Parse()

    // Show version
    if *version {
        fmt.Printf("%v\n", Version)
        return
    }

    // Encrypt
    if *encryptPass != "" {
        passwd, err := encrypt(*encryptPass)
        if err != nil {
            log.Fatalf("[error] %v", err)
        }
        log.Printf("[pass] %s", passwd)
        return
    }

    // Logging settings
    if *lgFile != "" || *plugin != "" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   *lgFile,
            MaxSize:    *logMaxSize,    // megabytes after which new file is created
            MaxBackups: *logMaxBackups, // number of backups
            MaxAge:     *logMaxAge,     // days
            Compress:   *logCompress,   // using gzip
        })
    }

    // loading configuration file
    cfg, err := loadConfigFile(*cfFile, *decryptPass)
    if err != nil {
        log.Fatalf("[error] reading config file: %v", err)
    }

    if len(cfg.Connections.URLs) == 0 {
        cfg.Connections.URLs = cfg.Global.URLs
    }
    if cfg.Connections.ContentEncoding == "" {
        cfg.Connections.ContentEncoding = cfg.Global.ContentEncoding
    }
    if cfg.Connections.Username == "" {
        cfg.Connections.Username = cfg.Global.Username
    }
    if cfg.Connections.Password == "" {
        cfg.Connections.Password = cfg.Global.Password
    }

    // Channel preparation
    statusChan = make(chan config.SockTable, 10000)
    webhookChan = make(chan Output, 10000)
    clnt := client.HttpConfig{
        URLs: randURLs(cfg.Connections.URLs),
        ContentEncoding: cfg.Connections.ContentEncoding,
        Username: cfg.Connections.Username,
        Password: cfg.Connections.Password,
    }
    go sendStatus(clnt, cfg, *debug)
    go sendWebhook(clnt, cfg, *debug)

    // Set Interval
    connectionsInterval, _ := time.ParseDuration(cfg.Connections.Interval)
    if connectionsInterval == 0 {
        log.Fatal("[error] setting connection interval: invalid duration")
    }

    run := true

    // Program signal processing
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
    go func() {
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
    go func(clnt client.HttpConfig) {
        for {
            getConnections(clnt, cfg, getHostname(), *debug)

            items := cacheRecords.Items()

            if *debug {
                log.Printf("[debug] check started, records in cache (%v)", len(items))
                for _, nr := range items {
                    log.Printf(
                        "[debug] cache: read record name=%s,ip=%s,port=%d,mode=%s,result=%d,response=%f",
                        nr.RemoteAddr.Name, nr.RemoteAddr.IP, nr.Relation.Port, nr.Relation.Mode, nr.Relation.Result, nr.Relation.Response,
                    )
                }
            }

            // Get records
            for _, nr := range items {

                if nr.Options.Status == "disabled" {
                    continue
                }

                if nr.State != "" {
                    continue
                }

                go func(nr config.SockTable) {

                    cacheRecords.SetState(nr.Id, "running")

                    result := int32(0)
                    response := float32(0)
                    trace := nr.Relation.Trace

                    tags := map[string]string{
                        "src_name":   nr.LocalAddr.Name,
                        "src_ip":     nr.LocalAddr.IP,
                        "dst_name":   nr.RemoteAddr.Name,
                        "dst_ip":     nr.RemoteAddr.IP,
                        "port":       fmt.Sprintf("%v", nr.Relation.Port),
                        "mode":       nr.Relation.Mode,
                        "service":    nr.Options.Service,
                        "status":     nr.Options.Status,
                        "account_id": fmt.Sprintf("%v", nr.Options.AccountID),
                    }
                    timeout := time.Duration(nr.Options.Timeout) * time.Second

                    switch  {

                    case nr.Relation.Mode == "tcp" || nr.Relation.Mode == "udp":
                        address := fmt.Sprintf("%v:%v", nr.RemoteAddr.IP, nr.Relation.Port)
                        result, response = dialTimeout(nr.Relation.Mode, address, timeout)

                        if result == 1 || response > nr.Options.MaxRespTime || nr.Relation.Trace == 2 {
                            if nr.Relation.Trace == 0 && nr.Options.Command != "" {
                                trace = 1
                                go runTrace(nr.Id, nr.Options.Command, "netmapTraceroute", tags, clnt)
                            }
                            if nr.Relation.Trace == 2 && nr.Options.Command != "" {
                                trace = 1
                                go runTrace(nr.Id, nr.Options.Command, "netmapCustomCommand", tags, clnt)
                            }
                        }

                    case nr.Relation.Mode == "cmd":
                        cmd := newTemplate(nr.Relation.Command, tags)

                        if cmd != "" {
                            for {
                                result, err = runCmdWithOutput(nr.Id, cmd, timeout, *debug)
                                if result != 2 || err != nil {
                                    if err != nil {
                                        log.Printf("[error] running: %v", err)
                                    }
                                    break
                                }
                            }
                            if nr.Relation.Type == "drop" {
                                if *debug {
                                    log.Printf("[debug] DELETE - /api/v1/netmap/records (%v)", nr.Id)
                                }
                                if err = httpClient.DelRecords(clnt, "/api/v1/netmap/records", []byte("[\""+nr.Id+"\"]")); err != nil {
                                    log.Printf("[error] %v - /api/v1/netmap/records", err)
                                }
                                return
                            }
                        }

                    }

                    if result == 0 && response < nr.Options.MaxRespTime && nr.Relation.Trace != 2 {
                        trace = 0
                    }

                    if nr.Options.Service == "" {
                        nr.Options.Service = "unknown"
                    }

                    nr.Options.AccountID = cfg.Global.AccountID
                    nr.Relation.Result = result
                    nr.Relation.Response = response
                    nr.Relation.Trace = trace

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

                    err := cacheRecords.Set(config.GetIdRec(&nr), nr)
                    if err != nil {
                        log.Printf("[error] %v", err)
                        return
                    }

                    select {
                    case statusChan <- nr:
                        //log.Printf("[debug] len chan - %v", len(api.Collect))
                    default: 
                        // Канал переполнен, можно удалить или игнорировать
                    }

                    cacheRecords.SetState(nr.Id, "")

                }(nr)
            }

            time.Sleep(connectionsInterval)
        }
    }(clnt)

    // Netstat run cmd
    go func() {

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
            Username: cfg.Netstat.Username,
            Password: cfg.Netstat.Password,
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
            options := config.Options{
                Status:      cfg.Netstat.Status,
                Timeout:     float32(netstatTimeout / time.Second),
                MaxRespTime: float32(netstatMaxRespTime / time.Second),
                AccountID:   cfg.Global.AccountID,
            }

            ihosts := cfg.Netstat.IgnoreHosts
            exists := map[string]bool{}

            for _, nr := range cacheRecords.Items() {
                exists[nr.Id] = true
            }

            if *debug {
                log.Print("[debug] netstat started")
            }

            nrs, err := netstat.GetSocks(ihosts, exists, options, cfg.Netstat.Incoming, *debug)
            if err != nil {
                log.Printf("[error] %v", err)
            }

            if len(nrs.Data) > 0 {
                jsn, err := json.Marshal(nrs)
                if err != nil {
                    log.Printf("[error] %v", err)
                } else {
                    if *debug {
                        log.Printf("[debug] POST - /api/v1/netmap/netstat (%v)", len(nrs.Data))
                    }
                    if err = httpClient.WriteRecords(clnt, "/api/v1/netmap/netstat", jsn); err != nil {
                        log.Printf("[error] %v - /api/v1/netmap/netstat", err)
                    }
                }
            }

            time.Sleep(netstatInterval)
        }
    }()

    // Daemon mode
    for run {
        if *plugin == "telegraf" {
            run = false
        }

        time.Sleep(time.Duration(*interval) * time.Second)
    }

}
