package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
)

// parceCmd parses the command and returns splitted slice from "|"
func parseCmd(cmd string) ([]string, error) {
	var ret []string
	if strings.TrimSpace(cmd) == "" {
		return ret, nil
	}
	cmds := strings.Split(cmd, "|")
	for _, cmd := range cmds {
		if strings.Contains(cmd, "~") {
			home, err := getHomeDir()
			if err != nil {
				return nil, err
			}
			cmd = strings.Replace(cmd, "~", home, 1)
		}
		ret = append(ret, strings.Join(strings.Fields(cmd), " "))
	}
	return ret, nil
}

func changeDirectory(path string) error {
	if strings.HasPrefix(path, "~") {
		home, err := getHomeDir()
		if err != nil {
			return err
		}
		path = strings.Replace(path, "~", home, 1)
	}
	err := os.Chdir(path)
	if err != nil {
		return err
	}
	return nil
}

func getHomeDir() (string, error) {
	us, err := user.Current()
	if err != nil {
		return "", err
	}
	return us.HomeDir, nil
}

// runCmd runs the command
func runCmd(in string, b *bytes.Buffer, last bool) (<-chan bytes.Buffer, <-chan error) {

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
		if last && path == "less" {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

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
	return fmt.Sprintf("\033[93m\033[1m%s@%s:~%s$ \033[0m", u.Username, hostName, cwd), err

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

var builtIn = map[string]func(string) error{
	"cd":    changeDirectory,
	"alias": alias,
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

func checkBuiltin(cmd string) (string, string, func(string) error) {
	var param string
	fields := strings.Fields(cmd)
	// fmt.Printf("checkBuiltin: %#v\n", fields)
	if len(fields) >= 2 {
		param = strings.Join(fields[1:], " ")
		// return "", "", nil
	}
	function, ok := builtIn[fields[0]]
	if !ok {
		return "", "", nil
	}

	if len(fields) == 1 && fields[0] == "cd" {
		return fields[0], "~", function
	}

	if len(fields) == 1 && fields[0] == "alias" {
		return fields[0], "", function
	}
	return fields[0], param, function
}

func loadEshRC() error {
	home, err := getHomeDir()
	if err != nil {
		return err
	}
	path := home + "/.eshrc"
	// esherc := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		fmt.Println("here after open")
		return err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	err = dec.Decode(&aliases)
	if err != nil {
		fmt.Println("here after decoding")
		return err
	}
	return nil
}

var aliases map[string]string

func alias(parameter string) error {
	if parameter == "" {
		for k, v := range aliases {
			fmt.Println(k, "->", v)

		}
		return nil
	}
	fields := strings.Split(parameter, "=")
	key, value := fields[0], fields[1]
	err := saveEshRC(key, value)
	if err != nil {
		return err
	}
	return nil
}

func checkEshRC() (bool, error) {
	home, err := getHomeDir()
	if err != nil {
		return false, err
	}
	files, err := ioutil.ReadDir(home)
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if file.Name() == ".eshrc" {
			return true, nil
		}
	}
	return false, nil
}

func createEshRC() error {
	home, err := getHomeDir()
	if err != nil {
		return err
	}

	path := home + "/.eshrc"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	aliases := map[string]string{}
	aliases["ll"] = "ls -la"
	err = enc.Encode(aliases)
	if err != nil {
		return err
	}

	return nil
}

func saveEshRC(key, value string) error {
	home, err := getHomeDir()
	if err != nil {
		return err
	}
	path := home + "/.eshrc"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	aliases[key] = value
	err = enc.Encode(aliases)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	var prompt string
	prompt, err := getPrompt()
	if err != nil {
		fmt.Println("error while creating prompt: ", err)
		os.Exit(1)
	}

	found, err := checkEshRC()
	if err != nil {
		log.Fatalln("error while checking .eshrc:", err)
	}
	if !found {
		err = createEshRC()
		if err != nil {
			log.Fatalln("error while creating eshrc")
		}
	}

	err = loadEshRC()
	if err != nil {
		log.Fatalln("error while loading eshrc:", err)
	}

	r := bufio.NewScanner(os.Stdin)
	fmt.Print(prompt)
	for r.Scan() {
		in := r.Text()
		commands, err := parseCmd(in)
		if err != nil {
			fmt.Println(err)
			continue
		}
		length := len(commands)
		if length != 0 {
			var b bytes.Buffer
			var last bool

			for i, cmd := range commands {
				if (i + 1) == length {
					last = true
				}
				if c, ok := aliases[cmd]; ok {
					cmd = c
				}
				var filename string
				var f *os.File
				if command, param, function := checkBuiltin(cmd); command != "" {
					err := function(param)
					if err != nil {
						fmt.Println(err)
					}
					prompt, err = getPrompt()
					if err != nil {
						fmt.Println("error while creating prompt: ", err)
						os.Exit(1)
					}
					continue
				}
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

				ch, errc := runCmd(cmd, &b, last)
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
