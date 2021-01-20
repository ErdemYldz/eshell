package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// State contains the state of a terminal.
type State struct {
	state
}

type state struct {
	termios unix.Termios
}

const ioctlReadTermios = unix.TCGETS
const ioctlWriteTermios = unix.TCSETS

const keyTab = 9

var tabCount int

func tabCompletion(line string) ([]string, string) {
	words := strings.Split(line, " ")
	var word string
	if len(words) > 1 {
		word = words[1]
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalln("error while getting directory:", err)
	}
	// fmt.Println("dirname:", dir)
	info, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Fatalln("error while getting fileinfo:", err)
	}
	// fmt.Printf("info: %#v\n", info[0].Name())
	var options []string
	for _, v := range info {
		fn := v.Name()
		if strings.HasPrefix(fn, word) {
			// fmt.Printf("%s\t", v.Name())
			options = append(options, fn)
		}
	}
	return options, word
}

func makeRaw(fd int) (*State, error) {
	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	oldState := State{state{termios: *termios}}

	// This attempts to replicate the behaviour documented for cfmakeraw in
	// the termios(3) manpage.
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	// termios.Oflag &^= unix.OPOST
	termios.Oflag |= unix.ONLCR // this is for printing newlines properly
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, termios); err != nil {
		return nil, err
	}

	return &oldState, nil
}

func restore(fd int, state *State) error {
	return unix.IoctlSetTermios(fd, ioctlWriteTermios, &state.termios)
}

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

func pipeline(file *os.File, cmds ...*exec.Cmd) error {
	last := len(cmds) - 1
	for i, cmd := range cmds[:last] {
		var err error
		if cmds[i+1].Stdin, err = cmd.StdoutPipe(); err != nil {
			return err
		}

		cmd.Stderr = os.Stderr
	}
	if file != nil {
		cmds[last].Stdout, cmds[last].Stderr = file, os.Stderr
		defer file.Close()
	} else {
		cmds[last].Stdout, cmds[last].Stderr = os.Stdout, os.Stderr
	}

	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			return err
		}
	}

	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			return err
		}
	}

	return nil
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

	cwd = strings.Replace(cwd, u.HomeDir, "~", 1)

	return fmt.Sprintf("\033[1;91m%s@%s\033[0m:\033[1;94m%s\033[0m$ ", u.Username, hostName, cwd), err

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
	if len(fields) >= 2 {
		param = strings.Join(fields[1:], " ")
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
func loadEshRC() error {
	home, err := getHomeDir()
	if err != nil {
		return err
	}

	path := home + "/.eshrc"
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

	err = os.Setenv("SHELL", "qwerty")
	if err != nil {
		log.Fatalln("error while settng env:", err)
	}

	oldState, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalln("error while making raw terminal:", err)
	}
	// defer restore(0, oldState)

	t := term.NewTerminal(os.Stdin, prompt)

	handlekeypress := func(line string, pos int, key rune) (string, int, bool) {
		if key == keyTab && line != "" {
			tabCount++
			if tabCount > 1 {
				fmt.Println("")
				options, word := tabCompletion(line)
				if len(options) == 1 {
					newline := strings.Replace(line, word, options[0], 1)
					t.Write([]byte("\n"))
					return newline, pos, true // true yazmasını engelliyor
				}
				for _, v := range options {
					fmt.Printf("%s\t", v)
				}
				fmt.Println("")
			}
			t.Write([]byte("\n"))
			return line, pos, false // true yazmasını engelliyor
		}
		return line, pos, false
	}
	t.AutoCompleteCallback = handlekeypress

	for {
		in, err := t.ReadLine()
		if err == io.EOF {
			fmt.Println("in io.EOF:", err)
			restore(0, oldState)
			os.Exit(1)
		}
		if err != nil {
			log.Println("error in t.ReadLine():", err)
		}
		if in == "exit" {
			restore(0, oldState)
			break
		}
		commands := parseCmd(in)
		if len(commands) != 0 {
			var pipeCmds []*exec.Cmd
			var f *os.File
			for _, cmd := range commands {
				if c, ok := aliases[cmd]; ok {
					cmd = c
				}

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

					t.SetPrompt(prompt)
					continue
				}

				if token, flag, ok := checkCommand(cmd); ok {
					reds := parseRedirections(cmd, token)
					cmd = reds[0]
					filename := reds[1]

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

				inSlice := strings.Fields(cmd)
				path := inSlice[0]
				args := inSlice[1:]

				c := exec.Command(path, args...)
				pipeCmds = append(pipeCmds, c)

			}
			if len(pipeCmds) > 0 {
				// Run the pipeline
				err = pipeline(f, pipeCmds...)
				if err != nil {
					log.Printf(": %s\n", err)
					t.Write([]byte(""))
				}
			}
		}
		// t.Write([]byte("\n"))
	}
}
