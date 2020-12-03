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
    "github.com/ltkh/telegraf-netmap/internal/http"
)

var (
	conn_chan = make(map[string](chan int))
)

type Connection struct {
	URLs           []string           `toml:"urls"`
	Username       string             `toml:"username"`
	Password       string             `toml:"password"`
	BearerToken    string             `toml:"bearer_token"`
	Headers        map[string]string  `toml:"headers"`
	TracerouteCmd  string             `toml:"traceroute_cmd"`
}

type Config struct {
	Connection     []Connection
}

// NetResponse struct
type NetResponse struct {
    Address        string             `json:"address"`
    Timeout        time.Duration      `json:"timeout"`
    ReadTimeout    time.Duration      `json:"read_timeout"`
    Protocol       string             `json:"protocol"`
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

	// We want to check the context error to see if the timeout was executed.
	// The error returned by cmd.Output() will be OS specific based on what
	// happens when a process is killed.
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("command timed out '%s'", scmd)
	}

	// If there's no context error, we know the command completed (or errored).
	fmt.Println("Output:", string(out))
	if err != nil {
		return nil, fmt.Errorf("non-zero exit code: %v '%s'", err, scmd)
	}

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
	
	//var cs Conns

    // Daemon mode
    for (run) {

		if *plugin == "telegraf" {
			run = false
		}

		var wg sync.WaitGroup

		for _, cn := range cfg.Connection {

			var nrs []NetResponse

			conn := http.New(http.HTTP{
				URLs:        cn.URLs,
				Username:    cn.Username,
				Password:    cn.Password,
				BearerToken: cn.BearerToken,
				Headers:     cn.Headers,
			})

			body, err := conn.GatherURL("GET", nil)
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
					tags := map[string]string{"server": host, "port": port, "protocol": n.Protocol}
					fields := map[string]interface{}{}

					// Gather data
					if n.Protocol == "tcp" {

						result, response := n.TCPGather()
						
						tags["protocol"] = n.Protocol
						fields["result_code"] = result
						fields["response_time"] = response

						if result == 1 && len(conn_chan[n.Address]) == 0 {

							if conn_chan[n.Address] == nil {
								conn_chan[n.Address] = make(chan int, 2)
							}
							conn_chan[n.Address] <- 1
							go func(){
								conn_chan[n.Address] <- 1
								tmpl, err := newTemplate(n.Address, cn.TracerouteCmd, tags)
								if err != nil {
									log.Printf("[error] %v", err)
								} else {
									out, err := runCommand(tmpl.String(), 300)
									if err != nil {
										log.Printf("[error] %v", err)
									} else {
										_, err = conn.GatherURL("POST", out)
										if err != nil {
											log.Printf("[error] %v", err)
										} 
									}
								}
								<- conn_chan[n.Address]
							}()
						}

						if result == 0 {
							<- conn_chan[n.Address]
							//close(conn_chan[n.Address])
						}

					//} else if n.Protocol == "udp" {
						//result, response = n.UDPGather()
					} else {
						log.Print("[error] bad protocol")
						return
					}

					// Add metrics
					if *plugin == "telegraf" {
						fmt.Printf("%s\n", addFields("netmap", fields, tags))
					}

				}(nr)
			}

		}

		wg.Wait()
        
		time.Sleep(time.Duration(*interval) * time.Second)

    }

}

