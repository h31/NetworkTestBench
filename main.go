package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const DEFAULT_LISTENING_ADDR string = "localhost:3456"

type TestCase struct {
	DelayTimeInMilliseconds int  `yaml:"DelayTimeInMilliseconds"`
	SegmentSize             int  `yaml:"SegmentSize"`
	DoReset                 bool `yaml:"DoReset"`
	ResetAfterNumOfSegments int  `yaml:"ResetAfterNumOfSegments"`
	NumOfClients            int  `yaml:"NumOfClients"`
}

type Settings struct {
	ListeningAddr    string   `yaml:"ListeningAddr"`
	ServerAddr       string   `yaml:"ServerAddr"`
	ClientCommand    string   `yaml:"ClientCommand"`
	WaitForClients   bool     `yaml:"WaitForClients"`
	ClientExecutable string   `yaml:"ClientExecutable"`
	ClientArgs       []string `yaml:"ClientArgs"`
}

var debugLog *log.Logger = log.New(os.Stderr, "DEBUG ", log.LstdFlags)
var debugIsEnabled = flag.Bool("d", false, "debugIsEnabled")
var testIndexOnly = flag.Int("t", -1, "only run test with a specified index")

var settings = Settings{}

func main() {
	prepareFlags()
	setUpLogging()
	action := parseSettings()
	switch action {
	case "collect":
		collect()
	case "test":
		test()
	default:
		fmt.Println("Unknown action:", action)
		os.Exit(1)
	}
}

func prepareFlags() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Syntax: %s [-d] [-t index] [collect|test]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
}

func parseSettings() string {
	switch {
	case flag.NArg() < 1:
		fmt.Println("Syntax: ./NetworkTestBench [collect|test]")
		os.Exit(1)
	case flag.NArg() == 1:
		debugLog.Println("Reading settings from settings.yaml")
		settingsFileContent, err := ioutil.ReadFile("settings.yaml")
		if err != nil {
			log.Fatal(err)
		}
		err = yaml.UnmarshalStrict(settingsFileContent, &settings)
		if err != nil {
			log.Fatal(err)
		}

		fields := strings.Fields(settings.ClientCommand)
		resolvedPath, _ := exec.LookPath(fields[0])
		settings.ClientExecutable = resolvedPath
		settings.ClientArgs = fields[1:]
	default:
		debugLog.Printf("NArg = %d, reading settings from command line arguments", flag.NArg())
		settings.ListeningAddr = DEFAULT_LISTENING_ADDR
		settings.ServerAddr = flag.Arg(1)
		settings.ClientExecutable = flag.Arg(2)
		settings.ClientArgs = flag.Args()[3:]
	}

	return flag.Arg(0)
}

func setUpLogging() {
	logFile, err := os.Create("testbench.log")
	if err != nil {
		panic(err)
	}
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	if !*debugIsEnabled {
		debugLog.SetOutput(logFile)
	}
}

func runClientCommand(stdin io.Reader, stopSignal chan bool) {
	cmd := exec.Command(settings.ClientExecutable, settings.ClientArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	debugLog.Println("Executing a client command...")
	cmd.Run()
	debugLog.Println("Client command completed")
	stopSignal <- true
}

func collect() {
	//sigs := make(chan os.Signal, 1)

	destAddr := getDestinationAddress()

	zeroTestCase := TestCase{
		DelayTimeInMilliseconds: 0,
		SegmentSize:             1460, // MSS for MTU = 1500
		DoReset:                 false,
		ResetAfterNumOfSegments: 0,
		NumOfClients:            1,
	}

	l := startListening()
	defer l.Close()

	//var userInput bytes.Buffer
	userInput, err := os.Create("userInput.txt")
	if err != nil {
		panic(err)
	}
	userInputBuffered := bufio.NewWriter(userInput)
	stdin := io.TeeReader(os.Stdin, userInput)
	redirectorStopSignal := make(chan bool)
	executionStopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	go acceptTCPConnections(l, destAddr, currentTestCase, redirectorStopSignal)
	go runClientCommand(stdin, executionStopSignal)
	currentTestCase <- &zeroTestCase

	waitForEvent(redirectorStopSignal, executionStopSignal)
	err = userInputBuffered.Flush()
	if err != nil {
		panic(err)
	}
	l.Close()
}

func waitForEvent(redirectorStopSignal chan bool, executionStopSignal chan bool) {
	if settings.WaitForClients {
		go devNullChannelReceiver(redirectorStopSignal, "redirector")
		<-executionStopSignal
		debugLog.Println("Received stop signal from the executor")
	} else {
		go devNullChannelReceiver(executionStopSignal, "executor")
		<-redirectorStopSignal
		debugLog.Println("Received stop signal from the redirector")
	}
}

func startListening() *net.TCPListener {
	addr, err := net.ResolveTCPAddr("tcp", settings.ListeningAddr)
	if err != nil {
		panic(err)
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatalln("Error listening:", err.Error())
	}
	debugLog.Println("Listening on", settings.ListeningAddr)
	return l
}

func acceptTCPConnections(l *net.TCPListener, destAddr *net.TCPAddr, currentTestCase chan *TestCase, stopSignal chan bool) {
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			debugLog.Fatalln("Error accepting: ", err)
		}
		go handleRequest(conn, destAddr, currentTestCase, stopSignal)
	}
}

func test() {
	destAddr := getDestinationAddress()

	l := startListening()
	defer l.Close()
	input, err := ioutil.ReadFile("userInput.txt")
	if err != nil {
		log.Fatalf("%s, please run the 'collect' action first", err)
	}
	redirectorStopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	testCases := readTestCasesSimple()
	go acceptTCPConnections(l, destAddr, currentTestCase, redirectorStopSignal)

	for testCaseNumber, testCase := range testCases {
		debugLog.Printf("Received a test case number %d", testCaseNumber)
		log.Printf("Current test config is number %d with %+v\n", testCaseNumber, testCase)
		if testCase.NumOfClients == 0 {
			testCase.NumOfClients = 1 // TODO: Dirty
		}
		executionStopSignal := make(chan bool)
		for i := 0; i < testCase.NumOfClients; i++ {
			stdin := bytes.NewReader(input)
			go runClientCommand(stdin, executionStopSignal)
		}
		for i := 0; i < testCase.NumOfClients; i++ {
			currentTestCase <- &testCase
			waitForEvent(redirectorStopSignal, executionStopSignal)
		}
	}
}

func devNullChannelReceiver(channel chan bool, source string) {
	for _ = range channel {
		debugLog.Printf("Received a message from the %s, ignoring it", source)
	}
}

func getDestinationAddress() *net.TCPAddr {
	destAddrText := settings.ServerAddr
	destAddr, err := net.ResolveTCPAddr("tcp", destAddrText)
	if err != nil {
		panic(err)
	}
	return destAddr
}

func handleRequest(serverConn *net.TCPConn, destAddr *net.TCPAddr, currentTestCase chan *TestCase, stopSignal chan bool) {
	testCase := <-currentTestCase
	debugLog.Println("Connection handler was started")

	clientConn, err := net.DialTCP("tcp", nil, destAddr)
	if err != nil {
		log.Println("Error dialing:", err.Error())
		log.Fatalln("Are you sure your server is up and running?")
	}

	serverConn.SetNoDelay(true)
	clientConn.SetNoDelay(true)
	serverConn.SetLinger(0)
	clientConn.SetLinger(0)
	stopSignalLocal := make(chan bool, 2)

	go transferData(serverConn, clientConn, testCase, stopSignalLocal, "client to server")
	go transferData(clientConn, serverConn, testCase, stopSignalLocal, "server to client")

	<-stopSignalLocal
	debugLog.Println("Received stop signal from the handler")
	stopSignal <- true
}

func transferData(source *net.TCPConn, destination *net.TCPConn, testCase *TestCase, stopSignal chan bool, description string) {
	transmissionCount := 0
	for {
		if testCase.DelayTimeInMilliseconds > 0 {
			time.Sleep(time.Duration(testCase.DelayTimeInMilliseconds) * time.Millisecond)
		}
		if testCase.DoReset && transmissionCount >= testCase.ResetAfterNumOfSegments {
			stopSignal <- true
			source.Close()
			destination.Close()
		}
		transmissionCount++
		buffer := make([]byte, testCase.SegmentSize)
		receivedLength, err := source.Read(buffer)
		if err == io.EOF {
			debugLog.Println("Received EOF in", description)
			stopSignal <- true
			destination.Close()
			break
		} else if err != nil && strings.HasSuffix(err.Error(), "use of closed network connection") {
			debugLog.Println("Use of closed network connection in", description)
			stopSignal <- true
			destination.Close()
			break
		} else if err != nil {
			log.Println("Error reading:", err.Error())
			break
		}
		_, err = destination.Write(buffer[0:receivedLength])
		if err != nil {
			log.Println("Error writing:", err.Error())
			stopSignal <- true
			source.Close()
			destination.Close()
		}
	}
}

func readTestCasesSimple() []TestCase {
	testCasesYaml, err := ioutil.ReadFile("testCases.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var testCases []TestCase
	err = yaml.Unmarshal(testCasesYaml, &testCases)
	if err != nil {
		log.Fatal(err)
	}
	debugLog.Println("Decoded an array")
	if *testIndexOnly >= 0 {
		testCases = testCases[*testIndexOnly : *testIndexOnly+1]
	}
	return testCases
}
