package main

import (
    "fmt"
	//"bytes"
	//"io"
    //"io/ioutil"
    "net"
	"time"
)

func main() {
	conn, err := net.DialTimeout("tcp", "google.com:80", 5 * time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

    fmt.Fprintf(conn, "PING\r\n")

	buffer := make([]byte, 1024)
	bytesRead, err := conn.Read(buffer)
	if bytesRead == 0 || err != nil {
		conn.Close()
		//fmt.Print(err)
		return
	}

	fmt.Println(string(buffer))

    //buf := make([]byte, 1024)
	//_, err = conn.Read(buf)

	//fmt.Fprintf(conn, "\r\n")
	//b, _ := ioutil.ReadAll(conn)
	//var buf bytes.Buffer
    //io.Copy(&buf, conn)
	//fmt.Print(buf.String())
}