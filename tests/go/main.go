package main

import (
	"bufio"
	"fmt"
	"os/exec"
)

func main() {
	cmd := exec.Command("ping", "-c", "10", "localhost")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println("Output:", line)
	}

	fmt.Println("Завершилось выполнение команды")

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading stdout:", err)
	}

	err = cmd.Wait()
	if err != nil {
		fmt.Println("Command finished with error:", err)
	}

}
