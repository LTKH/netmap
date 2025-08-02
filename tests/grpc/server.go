package main

import (
    "context"
    "log"
    "net"
    "time"

    pb "path/to/your/generated/proto"

    "google.golang.org/grpc"
)

type server struct {
    pb.UnimplementedEventServiceServer
    // Можно хранить список подписчиков или канал для рассылки событий
    subscribers map[chan *pb.Event]struct{}
}

func NewServer() *server {
    return &server{
        subscribers: make(map[chan *pb.Event]struct{}),
    }
}

// Метод подписки
func (s *server) Subscribe(req *pb.SubscribeRequest, stream pb.EventService_SubscribeServer) error {
    ch := make(chan *pb.Event, 10)
    s.subscribers[ch] = struct{}{}

    // Удаляем подписчика при завершении функции
    defer func() {
        delete(s.subscribers, ch)
        close(ch)
    }()

    for {
        select {
        case event := <-ch:
            if err := stream.Send(event); err != nil {
                log.Println("Error sending event:", err)
                return err
            }
        case <-stream.Context().Done():
            return nil
        }
    }
}

// Функция для рассылки событий всем подписчикам
func (s *server) broadcast(event *pb.Event) {
    for ch := range s.subscribers {
        select {
        case ch <- event:
        default:
            // Канал переполнен, можно удалить или игнорировать
        }
    }
}

func main() {
    lis, err := net.Listen("tcp", ":50051")
    if err != nil {
        log.Fatalf("failed to listen: %v", err)
    }

    grpcServer := grpc.NewServer()
    srv := NewServer()

    pb.RegisterEventServiceServer(grpcServer, srv)

    go func() {
        // Генерируем события для примера
        for i := 0; i < 10; i++ {
            time.Sleep(time.Second)
            event := &pb.Event{
                Message:   "Event number " + strconv.Itoa(i),
                Timestamp: time.Now().Unix(),
            }
            srv.broadcast(event)
        }
    }()

    log.Println("gRPC server listening on :50051")
    if err := grpcServer.Serve(lis); err != nil {
        log.Fatalf("failed to serve: %v", err)
    }
}