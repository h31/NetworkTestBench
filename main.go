package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const LISTENING_ADDR string = "localhost:3456"

type TestCase struct {
	DelayTimeInMilliseconds int
	SegmentSize             int
	DoReset                 bool
	ResetAfterNumOfSegments int
	NumOfClients            int
}

var debugLog *log.Logger = log.New(os.Stderr, "DEBUG ", log.LstdFlags)
var debugIsEnabled = flag.Bool("d", false, "debugIsEnabled")

func main() {
	flag.Parse()
	if flag.NArg() < 3 {
		fmt.Println("Syntax: [collect|test] server_host:port client_command...")
		os.Exit(1)
	}
	setUpLogging()
	action := flag.Arg(0)
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

func runClientCommand(stdin io.Reader) {
	cmd := exec.Command(flag.Arg(2), flag.Args()[3:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	debugLog.Println("Executing a client command...")
	cmd.Run()
	debugLog.Println("Client command completed")
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
	stopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	go acceptTCPConnections(l, destAddr, currentTestCase, stopSignal)
	go runClientCommand(stdin)
	currentTestCase <- &zeroTestCase

	<-stopSignal
	debugLog.Println("Received stop")
	err = userInputBuffered.Flush()
	if err != nil {
		panic(err)
	}
	l.Close()
}

func startListening() *net.TCPListener {
	addr, err := net.ResolveTCPAddr("tcp", LISTENING_ADDR)
	if err != nil {
		panic(err)
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatalln("Error listening:", err.Error())
	}
	debugLog.Println("Listening on", LISTENING_ADDR)
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
	stopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	testCases := readTestCasesSimple()
	go acceptTCPConnections(l, destAddr, currentTestCase, stopSignal)

	for _, testCase := range testCases {
		debugLog.Println("Received test case!")
		log.Printf("Current test config is %+v\n", testCase)
		if testCase.NumOfClients == 0 {
			testCase.NumOfClients = 1 // TODO: Dirty
		}
		for i := 0; i < testCase.NumOfClients; i++ {
			stdin := bytes.NewReader(input)
			go runClientCommand(stdin)
		}
		for i := 0; i < testCase.NumOfClients; i++ {
			currentTestCase <- &testCase
			<-stopSignal
			debugLog.Println("Received stop")
		}
	}
}

func getDestinationAddress() *net.TCPAddr {
	destAddrText := flag.Arg(1)
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
	debugLog.Println("Received stop signal")
	stopSignal <- true
	//clientConn.Close()
	//serverConn.Close()
}

func transferData(source *net.TCPConn, destination *net.TCPConn, testCase *TestCase, stopSignal chan bool, description string) {
	transmissionCount := 0
	for {
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
		destination.Write(buffer[0:receivedLength])
		if testCase.DelayTimeInMilliseconds > 0 {
			time.Sleep(time.Duration(testCase.DelayTimeInMilliseconds) * time.Millisecond)
		}
	}
}

func readTestCases() []TestCase {
	testCases, _ := os.Open("testCases.json") // TODO: Err
	decoder := json.NewDecoder(testCases)

	var testCase []TestCase
	// decode an array value (Message)
	err := decoder.Decode(testCase)
	if err != nil {
		log.Fatal(err)
	}

	debugLog.Println("Decoded a value")
	return testCase
}

func readTestCasesSimple() []TestCase {
	testCasesJson, err := ioutil.ReadFile("testCases.json")
	if err != nil {
		log.Fatal(err)
	}
	var testCases []TestCase
	err = json.Unmarshal(testCasesJson, &testCases)
	if err != nil {
		log.Fatal(err)
	}
	debugLog.Println("Decoded an array")
	return testCases
}

func prettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}
