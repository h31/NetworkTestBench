package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
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

func runClientCommand(stdin io.Reader, stopSignal chan bool) {
	cmd := exec.Command(os.Args[3], os.Args[4:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	cmd.Run()
	//stopSignal <- true

	//println("Input is", userInput.String())
}

func collect() {
	//sigs := make(chan os.Signal, 1)

	clientAddr := os.Args[2]

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
	stdin := io.TeeReader(os.Stdin, userInput)
	stopSignal := make(chan bool)
	currentTestCase := make(chan *TestCase)
	go acceptConnections(l, clientAddr, currentTestCase, stopSignal)
	go runClientCommand(stdin, stopSignal)
	currentTestCase <- &zeroTestCase

	<-stopSignal
	fmt.Println("Received stop")
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

func acceptConnections(l *net.TCPListener, clientAddr string, testCase chan *TestCase, stopSignal chan bool) {
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			fmt.Println("Error accepting: ", err.Error())
			os.Exit(1)
		}
		go handleRequest(conn, clientAddr, testCase, stopSignal)
	}
}

func test() {
	clientAddr := os.Args[2]

	//testCase := TestCase{
	//	DelayTimeInMilliseconds: 200,
	//	SegmentSize:             2,
	//	DoReset:                 false,
	//	ResetAfterNumOfSegments: 10,
	//}
	l := startListening()
	//defer l.Close()
	input, err := ioutil.ReadFile("userInput.txt")
	if err != nil {
		panic(err)
	}
	stopSignal := make(chan bool)
	//testCasesStream := make(chan *TestCase)
	currentTestCase := make(chan *TestCase, 1)
	//go readTestCases(testCasesStream)
	testCases := readTestCasesSimple()
	go acceptConnections(l, clientAddr, currentTestCase, stopSignal)

	//for {
	//	select {
	//	case _ = <-stopSignal:
	//		fmt.Println("Received stop")
	//		//l.Close()
	//	case testCase := <-testCasesStream:
	//		fmt.Println("Received test case!")
	//		if testCase == nil {
	//			break
	//		} else {
	//			currentTestCase <- testCase
	//		}
	//
	//	}
	//}
	for _, testCase := range testCases {
		fmt.Println("Received test case!")
		currentTestCase <- &testCase
		stdin := bytes.NewReader(input)
		go runClientCommand(stdin, stopSignal)
		<-stopSignal
		fmt.Println("Received stop")
		//l.Close()
	}
}

func handleRequest(serverConn *net.TCPConn, clientAddr string, testCaseStream chan *TestCase, stopSignal chan bool) {
	testCase := <- testCaseStream
	fmt.Printf("Current test config is %+v\n", testCase)

	rAddr, err := net.ResolveTCPAddr("tcp", clientAddr)
	if err != nil {
		panic(err)
	}
	clientConn, err := net.DialTCP("tcp", nil, rAddr)
	if err != nil {
		fmt.Println("Error dialing:", err.Error())
	}
	//defer clientConn.Close()
	//defer serverConn.Close()

	serverConn.SetNoDelay(true)
	clientConn.SetNoDelay(true)
	serverConn.SetLinger(0)
	clientConn.SetLinger(0)

	for {
		buffer := make([]byte, testCase.SegmentSize)
		receivedLength, err := serverConn.Read(buffer)
		if err == io.EOF {
			fmt.Println("Received EOF!")
			stopSignal <- true
			clientConn.Close()
			break
		}
		if err != nil {
			fmt.Println("Error reading:", err.Error())
		}
		clientConn.Write(buffer[0:receivedLength])
		time.Sleep(time.Duration(testCase.DelayTimeInMilliseconds) * time.Millisecond)
	}
}

func readTestCases(testCasesStream chan *TestCase)  {
	testCases, _ := os.Open("testCases.json") // TODO: Err
	decoder := json.NewDecoder(testCases)
	skipToken(decoder)
	for decoder.More() {
		testCase := new(TestCase)
		// decode an array value (Message)
		err := decoder.Decode(testCase)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Decoded a value")
		testCasesStream <- testCase
	}
	skipToken(decoder)
	testCasesStream <- nil
}

func readTestCasesSimple() []TestCase {
	testCasesFile, _ := os.Open("testCases.json") // TODO: Err
	decoder := json.NewDecoder(testCasesFile)
	var testCases []TestCase
	err := decoder.Decode(&testCases)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Decoded an array")
	if decoder.More() {
		log.Fatal(err)
	}
	return testCases
}

func skipToken(decoder *json.Decoder) {
	_, err := decoder.Token()
	if err != nil {
		log.Fatal(err)
	}
}

func prettyPrint(i interface{}) string {
	s, _ := json.MarshalIndent(i, "", "\t")
	return string(s)
}
