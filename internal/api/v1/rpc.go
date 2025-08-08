package v1

import (
    "io"
    "log"
    "time"
    "context"
    "crypto/rand"
    "encoding/json"
    "encoding/base64"
    "github.com/ltkh/netmap/internal/db"
    "github.com/ltkh/netmap/internal/config"

    "google.golang.org/grpc"
    pb "github.com/ltkh/netmap/internal/grpc"
)

var (
    ServerId = randStringCrypto(20)
)

type Rpc struct {
    Debug        bool
    Peers        []string
    DB           *db.DbClient
}

type Server struct {
    pb.UnimplementedEventServiceServer
    subscribers map[chan *pb.Event]struct{}
    DB *db.DbClient
}

func randStringCrypto(n int) string {
    // Генерируем n байт
    b := make([]byte, n)
    rand.Read(b)
    // Кодируем в base64 или используем напрямую
    return base64.URLEncoding.EncodeToString(b)
}

func convertEvent(ev pb.Event) config.SockTable {
    rec := config.SockTable{
        Id:              ev.Id,    
        Timestamp:       ev.Timestamp,
        LocalAddr:       config.SockAddr{
            IP:          ev.LocalAddr.Ip,
            Name:        ev.LocalAddr.Name,
            Port:        ev.LocalAddr.Port,
        },
        RemoteAddr:      config.SockAddr{
            IP:          ev.RemoteAddr.Ip,
            Name:        ev.RemoteAddr.Name,
            Port:        ev.RemoteAddr.Port,
        },
        Relation:        config.Relation{
            Mode:        ev.Relation.Mode,
            Type:        ev.Relation.Type,
            Port:        ev.Relation.Port,
            Command:     ev.Relation.Command,
            Result:      ev.Relation.Result,
            Response:    ev.Relation.Response,
            Trace:       ev.Relation.Trace,
        },
        Options:         config.Options{
            Service:     ev.Options.Service,
            Status:      ev.Options.Status,
            Command:     ev.Options.Command,
            Timeout:     ev.Options.Timeout,
            MaxRespTime: ev.Options.MaxRespTime,
            AccountID:   ev.Options.AccountID,
            HostMask:    ev.Options.HostMask,
            IgnoreMask:  ev.Options.IgnoreMask,
        },
    }

    return rec
}

func convertRec(id, evnt string, rc config.SockTable) *pb.Event{
    event := &pb.Event{
        ServerId:        id,
        Event:           evnt,
        Id:              rc.Id,
        Timestamp:       rc.Timestamp,
        LocalAddr: &pb.SockAddr{
            Ip:          rc.LocalAddr.IP,
            Name:        rc.LocalAddr.Name,
            Port:        rc.LocalAddr.Port,
        },
        RemoteAddr: &pb.SockAddr{
            Ip:          rc.RemoteAddr.IP,
            Name:        rc.RemoteAddr.Name,
            Port:        rc.RemoteAddr.Port,
        },
        Relation: &pb.Relation{
            Mode:        rc.Relation.Mode,
            Type:        rc.Relation.Type,
            Port:        rc.Relation.Port,
            Command:     rc.Relation.Command,
            Result:      rc.Relation.Result,
            Response:    rc.Relation.Response,
            Trace:       rc.Relation.Trace,
        },
        Options: &pb.Options{
            Service:     rc.Options.Service,
            Status:      rc.Options.Status,
            Command:     rc.Options.Command,
            Timeout:     rc.Options.Timeout,
            MaxRespTime: rc.Options.MaxRespTime,
            AccountID:   rc.Options.AccountID,
            HostMask:    rc.Options.HostMask,
            IgnoreMask:  rc.Options.IgnoreMask,
        },
    }

    return event
}

func NewServer(db *db.DbClient) *Server {
    return &Server{
        subscribers: make(map[chan *pb.Event]struct{}),
        DB: db,
    }
}

func (rpc *Rpc) ApplyEvent(event *pb.Event) error {
    if rpc.Debug {
        jsn, err := json.Marshal(event)
        if err != nil {
            return err
        }
        log.Printf("[event] %v", string(jsn))
    }

    rc := convertEvent(*event)

    switch event.Event {
    case "setStatus":
        if err := db.DbClient.SaveStatus(*rpc.DB, rc); err != nil {
            return err
        }
    case "setTracert":
        if err := db.DbClient.SaveTracert(*rpc.DB, rc); err != nil {
            return err
        }
    case "setRecord":
        if err := db.DbClient.SaveRecord(*rpc.DB, rc); err != nil {
            return err
        }
    case "delRecord":
        if err := db.DbClient.DelRecord(*rpc.DB, rc.Id); err != nil {
            return err
        }
    case "setException":
        if err := db.DbClient.SaveException(*rpc.DB, rc); err != nil {
            return err
        }
    case "delException":
        if err := db.DbClient.DelException(*rpc.DB, rc.Id); err != nil {
            return err
        }
    }

    return nil
}

func (rpc *Rpc) RunClient(peer string) error {
    // Устанавливаем соединение
    conn, err := grpc.Dial(peer, grpc.WithInsecure())
    if err != nil {
        return err
    }
    defer conn.Close()

    // Базовый контекст для запуска операций
    ctx := context.Background()
    client := pb.NewEventServiceClient(conn)

    // Вызов метода для получения списка объектов
    resp, err := client.ListObjects(ctx, &pb.ListObjectsRequest{Timestamp: 0})
    if err != nil {
        return err
    }

    // Обработка списка объектов
    for {
        event, err := resp.Recv()
        if err == io.EOF {
            break // все объекты получены
        }
        if err != nil {
            return err
        }

        if event.ServerId == ServerId {
            continue
        }

        if err := rpc.ApplyEvent(event); err != nil {
            log.Printf("[error] %v", err)
        }
    }

    // Запуск стриминг подписки
    stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{})
    if err != nil {
        return err
    }

    for {
        event, err := stream.Recv()
        if err != nil {
            return err
        }

        if event.ServerId == ServerId {
            continue
        }
        
        if err := rpc.ApplyEvent(event); err != nil {
            log.Printf("[error] %v", err)
        }
    }
}

func (rpc *Rpc) RunGrpcClient() {
    for _, peer := range rpc.Peers {
        go func(peer string) {
            for {
                err := rpc.RunClient(peer)
                if err != nil {
                    log.Printf("[error] %v", err)
                    time.Sleep(5 * time.Second)
                } else {
                    // Если runClient завершился без ошибок, можно завершить цикл
                    break
                }
            }
        }(peer)
    }
}

// Метод подписки
func (s *Server) Subscribe(req *pb.SubscribeRequest, stream pb.EventService_SubscribeServer) error {
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
                log.Printf("[error] sending event: %v", err)
                return err
            }
        case <-stream.Context().Done():
            return nil
        }
    }
}

func (s *Server) ListObjects(req *pb.ListObjectsRequest, stream pb.EventService_ListObjectsServer) error {
    args := config.RecArgs{Timestamp: req.Timestamp}
    records, err := db.DbClient.LoadRecords(*s.DB, args)
    if err != nil {
        return err
    }

    for _, rec := range records {
        if err := stream.Send(convertRec(ServerId, "setRecord", rec)); err != nil {
            log.Printf("[error] sending event: %v", err)
            return err
        }
    }

    return nil
}

// Функция для рассылки событий всем подписчикам
func (s *Server) Broadcast(event *pb.Event) {
    for ch := range s.subscribers {
        select {
        case ch <- event:
        default:
            // Канал переполнен, можно удалить или игнорировать
        }
    }
}