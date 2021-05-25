package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func makeOrOpenFifo(name string, flag int, perm fs.FileMode) (*os.File, error) {
	// if it's not there, create it
	exists := false
	err := syscall.Mkfifo(name, uint32(perm.Perm()))
	if err != nil {
		if os.IsExist(err) {
			exists = true
		} else {
			return nil, err
		}
	}

	pipe, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}

	// in case we created the pipe, its good, return
	if !exists {
		return pipe, nil
	}

	// if the file was there, it might not be a pipe...
	info, err := pipe.Stat()
	if err != nil {
		pipe.Close()
		return nil, err
	}

	if mode := info.Mode(); mode&os.ModeNamedPipe == 0 {
		pipe.Close()
		return nil, os.ErrExist
	}

	return pipe, nil
}

func commandReader(pipe *os.File, input chan<- string) {
	for {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			input <- scanner.Text()
		}

		if err := scanner.Err(); err != nil {
			fmt.Println(err)
			return
		}
	}
}

func main() {
	argLength := len(os.Args[1:])
	if argLength < 3 || (argLength > 0 && (os.Args[1] == "-h" || os.Args[1] == "--help")) {
		fmt.Print("Usage:\n", os.Args[0], " /path/to/java -jar /path/to/server.jar [-arguments...]\n")
		os.Exit(1)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	input := make(chan string)

	// use O_NONBLOCK to open a pipe directly
	pipe, err := makeOrOpenFifo("minecraft.control", os.O_RDONLY|syscall.O_NONBLOCK, 0660)
	if err != nil {
		if os.IsExist(err) {
			log.Fatal("ERROR: minecraft.control exists and is not a pipe.")
		}
		log.Fatal("ERROR: Could not open minecraft.control for reading.")
	}
	// then disable blocking to wait for content
	syscall.SetNonblock(int(pipe.Fd()), false)
	defer pipe.Close()

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal("ERROR: Could not connect to subprocess stdin.")
	}

	err = cmd.Start()
	if err != nil {
		log.Fatal(err)
	}

	cmdWaiter := make(chan interface{})

	go func() {
		cmd.Wait()
		close(cmdWaiter)
	}()

	// start listening on the named pipe
	go commandReader(pipe, input)

	fmt.Println("Started")
	for {
		select {
		case <-cmdWaiter:
			fmt.Println("Console exit")
			os.Exit(0)
		case <-signals:
			fmt.Println("Signal exit")
			fmt.Fprintln(stdin, "stop")
			stdin.Close()
			<-cmdWaiter
			os.Exit(0)
		case command := <-input:
			fmt.Printf("%s:\n", command)
			fmt.Fprintln(stdin, command)
		}
	}
}
