package main

import (
    "context"
    //"fmt"
    "log"
    "time"
    "encoding/json"
    "google.golang.org/grpc"

    pb "github.com/ltkh/netmap/internal/grpc"
)

func runClient() error {
    // Устанавливаем соединение
    conn, err := grpc.Dial("localhost:8085", grpc.WithInsecure())
    if err != nil {
        return err
    }
    defer conn.Close()

    client := pb.NewEventServiceClient(conn)

    ctx := context.Background()

    // Предположим, у вас есть стриминг подписки
    stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{})
    if err != nil {
        return err
    }

    for {
        event, err := stream.Recv()
        if err != nil {
            return err
        }
        
        jsn, err := json.Marshal(event)
        if err != nil {
            return err
        }

        log.Printf("[info] %v", string(jsn))
    }
}

func main() {
    for {
        err := runClient()
        if err != nil {
            log.Printf("[error] %v", err)
            time.Sleep(5 * time.Second)
        } else {
            // Если runClient завершился без ошибок, можно завершить цикл
            break
        }
    }
}