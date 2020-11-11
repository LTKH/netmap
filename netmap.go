package main

import (
    "log"
    "time"
    "os"
    "os/signal"
    "syscall"
    "runtime"
    "flag"
    "sync"
    "net"
    "net/textproto"
    "bufio"
    "regexp"
    "fmt"
    "strings"
    "github.com/naoina/toml"
    "gopkg.in/natefinch/lumberjack.v2"
)

type ResultType uint64

const (
    Success          ResultType = 0
    Timeout                     = 1
    ConnectionFailed            = 2
    ReadFailed                  = 3
    StringMismatch              = 4
)

type Config struct {
    Server struct {
        URLs         []string         `toml:"urls"`
    }
    NetResponse      []NetResponse    `toml:"net_response"`
}

// NetResponse struct
type NetResponse struct {
    Address     string
    Timeout     time.Duration
    ReadTimeout time.Duration
    Send        string
    Expect      string
    Protocol    string
}

// TCPGather will execute if there are TCP tests defined in the configuration.
// It will return a map[string]interface{} for fields and a map[string]string for tags
func (n *NetResponse) TCPGather() (tags map[string]string, fields map[string]interface{}) {
    // Prepare returns
    tags = make(map[string]string)
    fields = make(map[string]interface{})
    // Start Timer
    start := time.Now()
    // Connecting
    conn, err := net.DialTimeout("tcp", n.Address, n.Timeout)
    // Stop timer
    responseTime := time.Since(start).Seconds()
    // Handle error
    if err != nil {
        if e, ok := err.(net.Error); ok && e.Timeout() {
            setResult(Timeout, fields, tags, n.Expect)
        } else {
            setResult(ConnectionFailed, fields, tags, n.Expect)
        }
        return tags, fields
    }
    defer conn.Close()
    // Send string if needed
    if n.Send != "" {
        msg := []byte(n.Send)
        conn.Write(msg)
        // Stop timer
        responseTime = time.Since(start).Seconds()
    }
    // Read string if needed
    if n.Expect != "" {
        // Set read timeout
        conn.SetReadDeadline(time.Now().Add(n.ReadTimeout))
        // Prepare reader
        reader := bufio.NewReader(conn)
        tp := textproto.NewReader(reader)
        // Read
        data, err := tp.ReadLine()
        // Stop timer
        responseTime = time.Since(start).Seconds()
        // Handle error
        if err != nil {
            setResult(ReadFailed, fields, tags, n.Expect)
        } else {
            // Looking for string in answer
            RegEx := regexp.MustCompile(`.*` + n.Expect + `.*`)
            find := RegEx.FindString(string(data))
            if find != "" {
                setResult(Success, fields, tags, n.Expect)
            } else {
                setResult(StringMismatch, fields, tags, n.Expect)
            }
        }
    } else {
        setResult(Success, fields, tags, n.Expect)
    }
    fields["response_time"] = responseTime
    return tags, fields
}

// UDPGather will execute if there are UDP tests defined in the configuration.
// It will return a map[string]interface{} for fields and a map[string]string for tags
func (n *NetResponse) UDPGather() (tags map[string]string, fields map[string]interface{}) {
    // Prepare returns
    tags = make(map[string]string)
    fields = make(map[string]interface{})
    // Start Timer
    start := time.Now()
    // Resolving
    udpAddr, err := net.ResolveUDPAddr("udp", n.Address)
    // Handle error
    if err != nil {
        setResult(ConnectionFailed, fields, tags, n.Expect)
        return tags, fields
    }
    // Connecting
    conn, err := net.DialUDP("udp", nil, udpAddr)
    // Handle error
    if err != nil {
        setResult(ConnectionFailed, fields, tags, n.Expect)
        return tags, fields
    }
    defer conn.Close()
    // Send string
    msg := []byte(n.Send)
    conn.Write(msg)
    // Read string
    // Set read timeout
    conn.SetReadDeadline(time.Now().Add(n.ReadTimeout))
    // Read
    buf := make([]byte, 1024)
    _, _, err = conn.ReadFromUDP(buf)
    // Stop timer
    responseTime := time.Since(start).Seconds()
    // Handle error
    if err != nil {
        setResult(ReadFailed, fields, tags, n.Expect)
        return tags, fields
    }

    // Looking for string in answer
    RegEx := regexp.MustCompile(`.*` + n.Expect + `.*`)
    find := RegEx.FindString(string(buf))
    if find != "" {
        setResult(Success, fields, tags, n.Expect)
    } else {
        setResult(StringMismatch, fields, tags, n.Expect)
    }

    fields["response_time"] = responseTime

    return tags, fields
}

func setResult(result ResultType, fields map[string]interface{}, tags map[string]string, expect string) {
    var tag string
    switch result {
    case Success:
        tag = "success"
    case Timeout:
        tag = "timeout"
    case ConnectionFailed:
        tag = "connection_failed"
    case ReadFailed:
        tag = "read_failed"
    case StringMismatch:
        tag = "string_mismatch"
    }

    tags["result"] = tag
    fields["result_code"] = uint64(result)
}

func addFields(measurement string, fields map[string]interface{}, tags map[string]string) string {

    var a_tags []string
    a_tags = append(a_tags, measurement)
    for k, v := range tags {
        a_tags = append(a_tags, fmt.Sprintf("%s=%v", k, v))
    }

    var a_fields []string
    for k, v := range fields {
        a_fields = append(a_fields, fmt.Sprintf("%s=%v", k, v))
    }

    return fmt.Sprintf("%s %s", strings.Join(a_tags, ","), strings.Join(a_fields, ","))
}

func main() {

    // Limits the number of operating system threads
    runtime.GOMAXPROCS(runtime.NumCPU())

    // Command-line flag parsing
    cfFile          := flag.String("config", "", "config file")
    lgFile          := flag.String("logfile", "", "log file")
    log_max_size    := flag.Int("log_max_size", 1, "log max size") 
    log_max_backups := flag.Int("log_max_backups", 3, "log max backups")
    log_max_age     := flag.Int("log_max_age", 10, "log max age")
    log_compress    := flag.Bool("log_compress", true, "log compress")
    flag.Parse()

    // Logging settings
    log.SetOutput(&lumberjack.Logger{
        Filename:   *lgFile,
        MaxSize:    *log_max_size,    // megabytes after which new file is created
        MaxBackups: *log_max_backups, // number of backups
        MaxAge:     *log_max_age,     // days
        Compress:   *log_compress,    // using gzip
    })

    c := make(chan os.Signal, 1)
    signal.Notify(c, syscall.SIGHUP)

    // Daemon mode
    for {
        <-c

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

        var wg sync.WaitGroup

        for _, n := range cfg.NetResponse {
            wg.Add(1)
            go func(n NetResponse) {
                defer wg.Done()

                // Prepare host and port
                host, port, err := net.SplitHostPort(n.Address)
                if err != nil {
                    log.Printf("[error] %v", err)
                    return 
                }
                if host == "" {
                    n.Address = "localhost:" + port
                }
                if port == "" {
                    log.Print("[error] bad port")
                    return
                }

                // Prepare data
                tags := map[string]string{"server": host, "port": port}
                var fields map[string]interface{}
                var returnTags map[string]string

                // Gather data
                if n.Protocol == "tcp" {
                    returnTags, fields = n.TCPGather()
                    tags["protocol"] = "tcp"
                } else if n.Protocol == "udp" {
                    returnTags, fields = n.UDPGather()
                    tags["protocol"] = "udp"
                } else {
                    log.Print("[error] bad protocol")
                }

                // Merge the tags
                for k, v := range returnTags {
                    tags[k] = v
                }

                // Add metrics
                fmt.Printf("%s\n", addFields("netmap", fields, tags))

            }(n)
        }

        wg.Wait()

    }

}

