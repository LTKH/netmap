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

	buffer := make([]byte, 0)
	bytesRead, err := conn.Read(buffer)
	if err != nil {
		conn.Close()
		fmt.Print(err)
		return
	}

	fmt.Println(bytesRead)

    //buf := make([]byte, 1024)
	//_, err = conn.Read(buf)

	//fmt.Fprintf(conn, "\r\n")
	//b, _ := ioutil.ReadAll(conn)
	//var buf bytes.Buffer
    //io.Copy(&buf, conn)
	//fmt.Print(buf.String())
}