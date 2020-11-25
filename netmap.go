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
    "encoding/json"
    "github.com/naoina/toml"
    "gopkg.in/natefinch/lumberjack.v2"
    "github.com/ltkh/telegraf-netmap/internal/http"
)

type Config struct {
    Connection  []http.HTTP      `toml:"connection"`
}

// NetResponse struct
type NetResponse struct {
    Address     string           `toml:"adress"`
    Timeout     time.Duration    `toml:"timeout"`
    ReadTimeout time.Duration    `toml:"read_timeout"`
    Protocol    string           `toml:"protocol"`
    TimeoutCmd  string           `toml:"timeout_cmd"`
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

func runCommand(cmd string) error {
    log.Printf("[info] running %s\n", cmd)
    var c *exec.Cmd
    if runtime.GOOS == "windows" {
        c = exec.Command("cmd", "/C", cmd)
    } else {
        c = exec.Command("/bin/sh", "-c", cmd)
    }

    output, err := c.CombinedOutput()
    if err != nil {
        log.Printf("[error] %q\n", string(output))
        return err
    }
    log.Printf("[info] %q\n", string(output))
    return nil
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

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	go func() {
        <-c
        run = true
    }()

    // Daemon mode
    for (run) {

		if *plugin == "telegraf" {
			run = false
		}

		var wg sync.WaitGroup

		for _, conn := range cfg.Connection {

			var nr []NetResponse

			h := http.New(conn)
			body, err := h.GatherURL()
			if err != nil {
				log.Printf("[error] %v", err)
			} else {
				if err := json.Unmarshal(body, &nr); err != nil {
					log.Printf("[error] error reading json from response body: %s", err)
				}
			}
			
			for _, n := range nr {
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
					fields := map[string]interface{}{}

					// Gather data
					if n.Protocol == "tcp" {

						result, response := n.TCPGather()
						
						tags["protocol"] = n.Protocol
						fields["result_code"] = result
						fields["response_time"] = response
						
						//

						//runCommand(fmt.Sprintf("traceroute -p %d %s", port, host))

					} else if n.Protocol == "udp" {
						//returnTags, fields = n.UDPGather()
						//tags["protocol"] = "udp"
					} else {
						log.Print("[error] bad protocol")
						return
					}

					// Add metrics
					if *plugin == "telegraf" {
						fmt.Printf("%s\n", addFields("netmap", fields, tags))
					}

				}(n)
			}

		}

		wg.Wait()
        
		time.Sleep(time.Duration(*interval) * time.Second)

    }

}

