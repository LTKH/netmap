package main

import (
    "bufio"
    "fmt"
    "os/exec"
)

func main() {
    // Создаем команду
    cmd := exec.Command("ping", "-c", "10",  "localhost") // или любая другая команда

    // Получаем поток вывода
    stdoutPipe, err := cmd.StdoutPipe()
    if err != nil {
        panic(err)
    }

    // Запускаем команду
    if err := cmd.Start(); err != nil {
        panic(err)
    }

    // Читаем вывод в реальном времени
    scanner := bufio.NewScanner(stdoutPipe)
    for scanner.Scan() {
        line := scanner.Text()
        fmt.Println("Output:", line)
        // Можно добавить условие для выхода или обработки
    }

	fmt.Println("Завершилось выполнение команды")

    if err := scanner.Err(); err != nil {
        fmt.Println("Error reading stdout:", err)
    }

    // Ждем завершения команды (бесконечная команда обычно не завершится)
    err = cmd.Wait()
    if err != nil {
        fmt.Println("Command finished with error:", err)
    }
	
}