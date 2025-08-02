package main

import (
    "context"
    "fmt"
    "log"
    "time"

    pb "path/to/your/generated/proto"

    "google.golang.org/grpc"
)

func main() {
    conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
    if err != nil {
        log.Fatalf("did not connect: %v", err)
    }
    defer conn.Close()

    client := pb.NewEventServiceClient(conn)

    stream, err := client.Subscribe(context.Background(), &pb.SubscribeRequest{})
    if err != nil {
        log.Fatalf("Error subscribing: %v", err)
    }

    for {
        event, err := stream.Recv()
        if err != nil {
            log.Println("Stream closed:", err)
            break
        }
        fmt.Printf("Received event: %s at %d\n", event.Message, event.Timestamp)
    }
}