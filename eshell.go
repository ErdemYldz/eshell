package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// parceCmd parses the command and returns splitted slice from "|"
func parseCmd(cmd string) []string {
	var ret []string
	cmds := strings.Split(cmd, "|")
	for _, cmd := range cmds {
		ret = append(ret, strings.Join(strings.Fields(cmd), " "))
	}
	return ret
}

// runCmd runs the command
func runCmd(in string, b *bytes.Buffer) (<-chan bytes.Buffer, <-chan error) {

	ch := make(chan bytes.Buffer)
	errc := make(chan error)

	go func() {
		inSlice := strings.Fields(in)
		path := inSlice[0]
		args := inSlice[1:]

		cmd := exec.Command(path, args...)
		if b != nil {
			cmd.Stdin = strings.NewReader(b.String())
		}
		b.Reset()
		cmd.Stdout = b
		cmd.Stderr = b

		err := cmd.Run()
		errc <- err
		ch <- *b
		close(errc)
		close(ch)
	}()
	return ch, errc
}

// getPrompt returns the prompt string
func getPrompt() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	hostName, err := os.Hostname()
	if err != nil {
		return "", err
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s@%s:~%s$ ", u.Username, hostName, cwd), err

}

func main() {
	prompt, err := getPrompt()
	if err != nil {
		fmt.Println("error while creating prompt: ", err)
		os.Exit(1)
	}

	r := bufio.NewScanner(os.Stdin)
	fmt.Print(prompt)
	for r.Scan() {
		in := r.Text()
		var b bytes.Buffer
		for _, cmd := range parseCmd(in) {
			bufChan, errc := runCmd(cmd, &b)
			if err := <-errc; err != nil {
				fmt.Println("error in runCmd: ", err)
			}
			b = <-bufChan
		}
		fmt.Println(b.String())

		fmt.Print(prompt)
	}
}
