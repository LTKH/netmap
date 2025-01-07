#!/bin/bash
go build -o bin/netserver cmd/netserver/netserver.go
bin/netserver -listen.client-address='127.0.0.1:8084' -listen.peer-address='127.0.0.1:8085' -initial-cluster='127.0.0.1:8085,127.0.0.1:8087' & 
pid1=$!
bin/netserver -listen.client-address='127.0.0.1:8086' -listen.peer-address='127.0.0.1:8087' -initial-cluster='127.0.0.1:8085,127.0.0.1:8087' &
pid2=$!

k6 run tests/k6/netserver_records.js
k6 run tests/k6/netserver_status.js
k6 run tests/k6/netserver_netstat.js

kill $pid1 $pid2 