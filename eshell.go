package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
)

// parceCmd parses the command and returns splitted slice from "|"
func parseCmd(cmd string) []string {
	var ret []string
	if strings.TrimSpace(cmd) == "" {
		return ret
	}
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

func makeFile(filename string, flag int) (*os.File, error) {
	f, err := os.OpenFile(filename, flag, 0666)
	if err != nil {
		return nil, err
	}
	return f, nil
}

var tokens = map[string]int{
	">":  os.O_CREATE | os.O_WRONLY,
	">>": os.O_APPEND | os.O_CREATE | os.O_WRONLY,
}

// checks the command if it has > or >>
func checkCommand(cmd string) (string, int, bool) {
	for k, v := range tokens {
		// 3 because it must be bigger than the longest token
		s := regexp.MustCompile(k).Split(cmd, 3)
		if len(s) == 2 {
			return k, v, true
		}
	}
	return "", -1, false
}
func parseRedirections(cmd, token string) []string {
	var ret []string
	cmds := strings.Split(cmd, token)
	for _, cmd := range cmds {
		ret = append(ret, strings.Join(strings.Fields(cmd), " "))
	}
	return ret
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
		commands := parseCmd(in)
		if commands != nil {
			var b bytes.Buffer
			for _, cmd := range commands {
				var filename string
				var f *os.File
				if token, flag, ok := checkCommand(cmd); ok {
					reds := parseRedirections(cmd, token)
					fmt.Printf("after parsed reds: %#v\n", reds)
					cmd = reds[0]
					filename = reds[1]
					f, err = makeFile(filename, flag)
					if err != nil {
						fmt.Printf("error while creating %s:%s", filename, err)
					}
					if cmd == "" {
						if token == ">" {
							f.Truncate(0)
							f.Close()
							continue
						}
						if token == ">>" {
							f.Close()
							continue
						}
					}
				}

				ch, errc := runCmd(cmd, &b)
				if err := <-errc; err != nil {
					fmt.Println(err)
				}
				if filename != "" {
					b = <-ch
					f.Write(b.Bytes())
					b.Reset()
					continue
				} else {
					b = <-ch
				}
			}
			fmt.Println(b.String())
		}

		fmt.Print(prompt)
	}

}
