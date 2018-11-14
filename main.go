package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Syntax: [collect|test] server_host:port client_command...")
		os.Exit(1)
	}
	action := os.Args[1]
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

func runClientCommand(stdin io.Reader) {
	cmd := exec.Command(os.Args[3], os.Args[4:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	cmd.Run()
	log.Println("Client command was finished")
}

func collect() {
	//sigs := make(chan os.Signal, 1)

	destAddr := getDestinationAddress()

	zeroTestCase := TestCase{
		DelayTimeInMilliseconds: 0,
		SegmentSize:             1460, // MSS for MTU = 1500
		DoReset:                 false,
		ResetAfterNumOfSegments: 0,
	}

	l := startListening()
	defer l.Close()

	//var userInput bytes.Buffer
	userInput, err := os.Create("userInput.txt")
	if err != nil {
		panic(err)
	}
	userInputBuffered := bufio.NewWriter(userInput)
	stdin := io.TeeReader(os.Stdin, userInputBuffered)
	stopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	go acceptTCPConnections(l, destAddr, currentTestCase, stopSignal)
	go runClientCommand(stdin)
	currentTestCase <- &zeroTestCase

	<-stopSignal
	fmt.Println("Received stop")
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
		fmt.Println("Error listening:", err.Error())
		os.Exit(1)
	}
	fmt.Println("Listening on ", LISTENING_ADDR)
	return l
}

func acceptTCPConnections(l *net.TCPListener, destAddr *net.TCPAddr, currentTestCase chan *TestCase, stopSignal chan bool) {
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			log.Fatalln("Error accepting: ", err)
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
	currentTestCase := make(chan *TestCase, 1)
	testCases := readTestCasesSimple()
	go acceptTCPConnections(l, destAddr, currentTestCase, stopSignal)

	for _, testCase := range testCases {
		fmt.Println("Received test case!")
		currentTestCase <- &testCase
		stdin := bytes.NewReader(input)
		go runClientCommand(stdin)
		<-stopSignal
		fmt.Println("Received stop")
	}
}

func getDestinationAddress() *net.TCPAddr {
	destAddrText := os.Args[2]
	destAddr, err := net.ResolveTCPAddr("tcp", destAddrText)
	if err != nil {
		panic(err)
	}
	return destAddr
}

func handleRequest(serverConn *net.TCPConn, destAddr *net.TCPAddr, currentTestCase chan *TestCase, stopSignal chan bool) {
	testCase := <-currentTestCase
	fmt.Printf("Current test config is %+v\n", testCase)

	clientConn, err := net.DialTCP("tcp", nil, destAddr)
	if err != nil {
		fmt.Println("Error dialing:", err.Error())
	}

	serverConn.SetNoDelay(true)
	clientConn.SetNoDelay(true)
	serverConn.SetLinger(0)
	clientConn.SetLinger(0)
	stopSignalLocal := make(chan bool, 2)

	go transferData(serverConn, clientConn, testCase, stopSignalLocal, "server to client")
	go transferData(clientConn, serverConn, testCase, stopSignalLocal, "client to server")

	<-stopSignalLocal
	stopSignal <- true
	//clientConn.Close()
	//serverConn.Close()
}

func transferData(source *net.TCPConn, destination *net.TCPConn, testCase *TestCase, stopSignal chan bool, description string) {
	for {
		buffer := make([]byte, testCase.SegmentSize)
		receivedLength, err := source.Read(buffer)
		if err == io.EOF {
			fmt.Println("Received EOF in", description)
			//stopSignal <- true
			//source.Close()
			break
		} else if err != nil && strings.HasSuffix(err.Error(), "use of closed network connection") {
			fmt.Println("Use of closed network connection in", description)
			//stopSignal <- true
			//source.Close()
			break
		} else if err != nil {
			fmt.Println("Error reading:", err.Error())
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

	fmt.Println("Decoded a value")
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
	fmt.Println("Decoded an array")
	return testCases
}

func prettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}
