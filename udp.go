package main

import (
	"log"
	"net"
	"time"
)

type UDPTestCase struct {
	DelayTimeInMilliseconds int  `yaml:"DelayTimeInMilliseconds"`
	SegmentSize             int  `yaml:"SegmentSize"`
	DoReset                 bool `yaml:"DoReset"`
	ResetAfterNumOfSegments int  `yaml:"ResetAfterNumOfSegments"`
	NumOfClients            int  `yaml:"NumOfClients"`
}

func getUDPDestinationAddress() *net.UDPAddr {
	destAddrText := settings.ServerAddr
	destAddr, err := net.ResolveUDPAddr("udp", destAddrText)
	if err != nil {
		panic(err)
	}
	return destAddr
}

var currentTestCase *TestCase

func updateCurrentTestCase(testCaseChannel chan *TestCase) {
	for testCase := range testCaseChannel {
		currentTestCase = testCase // TODO: Make atomic
	}
}

func acceptUDPConnections(testCaseChannel chan *TestCase, stopSignal chan bool) {
	debugLog.Println("Starting the UDP server")
	l, _ := net.ResolveUDPAddr("udp", settings.ListeningAddr) // TODO: Check errors
	destAddr := getUDPDestinationAddress()

	listeningConn, err := net.ListenUDP("udp", l)
	if err != nil {
		debugLog.Fatalln("Error listening: ", err)
	}
	go updateCurrentTestCase(testCaseChannel)
	sendToServer(listeningConn, destAddr, stopSignal)
	//go handleUDPRequest(listeningConn, destAddr, currentTestCase, stopSignal)
}

func stopSignalHandler(stopSignal chan bool, listeningConnection *net.UDPConn, connectionPool map[string]*net.UDPConn) {
	<-stopSignal
	time.Sleep(1 * time.Second)
	debugLog.Println("Going to flush delay buffers")
	for addr, buffer := range reorderBufferToClient {
		networkAddr, _ := net.ResolveUDPAddr("udp", addr) // TODO: Dirty
		sendDatagram(listeningConnection, buffer, networkAddr)
	}
	for addr, buffer := range reorderBufferToServer {
		sendDatagram(connectionPool[addr], buffer, nil)
	}
}

func sendToServer(listeningConn *net.UDPConn, destAddr *net.UDPAddr, stopSignal chan bool) {
	buffer := make([]byte, 65536) // Max IPv4 packet size
	connectionPool := make(map[string]*net.UDPConn)
	go stopSignalHandler(stopSignal, listeningConn, connectionPool)

	for {
		receivedLength, srcAddr, _ := listeningConn.ReadFromUDP(buffer)
		debugLog.Printf("Received datagram length %d from %s", receivedLength, srcAddr.String())
		serverConnection, exists := connectionPool[srcAddr.String()]
		if !exists {
			debugLog.Println("New source address")
			var err error
			serverConnection, err = net.DialUDP("udp", nil, destAddr)
			if err != nil {
				log.Fatalln("Failed to create UDP connection to the server: ", err.Error())
			}
			debugLog.Printf("Created a new connection to the server: %s", serverConnection.LocalAddr().String())
			connectionPool[srcAddr.String()] = serverConnection
			go sendToClient(serverConnection, listeningConn, srcAddr)
		}
		dispatchReorder(serverConnection, srcAddr.String(), nil, buffer, receivedLength, reorderBufferToServer)
	}
}

func sendToClient(connToServer *net.UDPConn, listeningConn *net.UDPConn, clientAddr *net.UDPAddr) {
	buffer := make([]byte, 65536) // Max IPv4 packet size
	for {
		receivedLength, _ := connToServer.Read(buffer)
		debugLog.Printf("Received datagram length %d from the server", receivedLength)
		dispatchReorder(listeningConn, clientAddr.String(), clientAddr, buffer, receivedLength, reorderBufferToClient)
	}
}

var reorderBufferToClient = make(map[string][]byte)
var reorderBufferToServer = make(map[string][]byte)

func dispatchReorder(connection *net.UDPConn, reorderID string, destination *net.UDPAddr, buffer []byte, bufferLength int, reorderBuffer map[string][]byte) {
	actualBytes := buffer[0:bufferLength]
	reorderBufferData, hasDelayedData := reorderBuffer[reorderID]
	switch {
	case currentTestCase.ReorderingEnabled && !hasDelayedData:
		debugLog.Printf("Delayed data to client %s", reorderID)
		storedBuffer := copyByteArray(actualBytes)
		reorderBuffer[reorderID] = storedBuffer
		return
	case currentTestCase.ReorderingEnabled && hasDelayedData:
		debugLog.Printf("Sent delayed data to client %s", reorderID)
		sendDatagram(connection, actualBytes, destination)
		sendDatagram(connection, reorderBufferData, destination)
		delete(reorderBuffer, reorderID)
	default:
		debugLog.Printf("Sent currently received data to client %s", reorderID)
		sendDatagram(connection, actualBytes, destination)
	}
	//_, _ = connection.WriteToUDP(actualBytes, clientAddr)
}

func copyByteArray(buffer []byte) []byte {
	storedBuffer := make([]byte, len(buffer))
	copy(storedBuffer, buffer)
	return storedBuffer
}

func sendDatagram(conn *net.UDPConn, data []byte, clientAddr *net.UDPAddr) {
	debugLog.Printf("Send datagram from %s to %s", conn.LocalAddr().String(), conn.RemoteAddr().String())
	if clientAddr == nil {
		conn.Write(data)
	} else {
		conn.WriteToUDP(data, clientAddr)
	}
}

func handleUDPRequest(serverConn *net.TCPConn, destAddr *net.TCPAddr, currentTestCase chan *TestCase, stopSignal chan bool) {
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
