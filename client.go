// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// client is a WebRTC client that receives video and saves it to file
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func main() {
	outputFile := flag.String("output", "received.h264", "Output video file (H264 format)")
	localIP := flag.String("ip", "", "Local IP address for WebRTC (e.g., 192.168.100.2). If not specified, auto-detect")
	answerFile := flag.String("answer-file", "", "Path to file to write answer (optional, if not specified, write to stdout)")
	maxDuration := flag.Duration("max-duration", 0, "Maximum recording duration (e.g., 30s, 5m). 0 means no limit")
	maxSize := flag.Int64("max-size", 0, "Maximum file size in MB (0 means no limit)")
	flag.Parse()

	// Create SettingEngine to configure port range and ICE timeouts
	settingEngine := webrtc.SettingEngine{}
	// Set fixed UDP port range for easier testing (use different range to avoid conflicts)
	if err := settingEngine.SetEphemeralUDPPortRange(50100, 50200); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to set port range: %v\n", err)
	}
	// Increase ICE timeouts for localhost testing
	settingEngine.SetICETimeouts(
		10*time.Second, // Disconnected timeout
		30*time.Second, // Failed timeout
		2*time.Second,  // Keepalive interval
	)

	// Configure NAT 1-to-1 IP mapping if IP is specified
	if *localIP != "" {
		// Verify that the IP is valid
		ip := net.ParseIP(*localIP)
		if ip == nil {
			fmt.Fprintf(os.Stderr, "Warning: Invalid IP address: %s, using auto-detect\n", *localIP)
		} else {
			settingEngine.SetNAT1To1IPs([]string{*localIP}, webrtc.ICECandidateTypeHost)
			fmt.Fprintf(os.Stderr, "Using specified IP address: %s\n", *localIP)
		}
	}

	// Prepare the configuration
	// For localhost testing, we don't need STUN servers
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// Empty - rely only on host candidates for localhost communication
		},
	}

	// Create API with SettingEngine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", cErr)
		}
	}()

	// Set a handler for when a new remote track starts
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				defer ticker.Stop()
				for range ticker.C {
					// Check if connection is closed
					if peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
						return
					}
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}) // nolint
					if rtcpSendErr != nil {
						// If connection is closed, stop sending PLI
						if strings.Contains(rtcpSendErr.Error(), "closed") {
							return
						}
						// Only log non-closed errors
						fmt.Fprintf(os.Stderr, "Error sending RTCP PLI: %v\n", rtcpSendErr)
					}
				}
			}()
		}

		codecName := strings.ToLower(strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1])
		fmt.Fprintf(os.Stderr, "Track has started, of type %d: %s \n", track.PayloadType(), codecName)

		if codecName == "h264" {
			// Write H264 data to file
			writeH264ToFile(track, *outputFile, *maxDuration, *maxSize)
		} else {
			fmt.Fprintf(os.Stderr, "Unsupported codec: %s, only H264 is supported\n", codecName)
		}
	})

	// Set the handler for ICE candidate
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			fmt.Fprintf(os.Stderr, "ICE Candidate: %s\n", candidate.String())
		} else {
			fmt.Fprintf(os.Stderr, "ICE Candidate gathering completed\n")
		}
	})

	// Set the handler for ICE connection state
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: ICE connection failed!\n")
		}
	})

	// Set the handler for Peer connection state
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: Peer connection failed!\n")
		}
	})

	// Wait for the offer from stdin
	offer := webrtc.SessionDescription{}
	decode(readUntilNewline(), &offer)

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// Create an answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	<-gatherComplete

	// Output the answer in base64 to stdout or file
	answerStr := encode(peerConnection.LocalDescription())
	if *answerFile != "" {
		err := os.WriteFile(*answerFile, []byte(answerStr+"\n"), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing answer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Answer written to file: %s (%d bytes)\n", *answerFile, len(answerStr))
	} else {
		fmt.Println(answerStr)
	}

	// Block forever
	select {}
}

func writeH264ToFile(track *webrtc.TrackRemote, filename string, maxDuration time.Duration, maxSizeMB int64) {
	file, err := os.Create(filename)
	if err != nil {
		panic(fmt.Sprintf("Failed to create output file: %v", err))
	}
	defer file.Close()

	writer := bufio.NewWriterSize(file, 64*1024)
	defer writer.Flush()

	packetCount := 0
	bytesWritten := int64(0)
	lastFlushTime := time.Now()
	startTime := time.Now()
	maxSizeBytes := maxSizeMB * 1024 * 1024

	// Annex-B start code
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// Buffer for reassembling fragmented NAL units (FU-A)
	var fuBuffer []byte
	var fuNALType byte

	fmt.Fprintf(os.Stderr, "Writing H264 stream to %s...\n", filename)
	fmt.Fprintf(os.Stderr, "Parsing RTP payload and adding Annex-B start codes\n")
	if maxDuration > 0 {
		fmt.Fprintf(os.Stderr, "Max duration: %v\n", maxDuration)
	}
	if maxSizeMB > 0 {
		fmt.Fprintf(os.Stderr, "Max size: %d MB\n", maxSizeMB)
	}

	lastReadTime := time.Now()
	readTimeout := 5 * time.Second

	writeNALUnit := func(nalData []byte) error {
		if len(nalData) == 0 {
			return nil
		}
		// Write start code
		if _, err := writer.Write(startCode); err != nil {
			return err
		}
		// Write NAL unit data
		n, err := writer.Write(nalData)
		if err != nil {
			return err
		}
		bytesWritten += int64(len(startCode) + n)
		return nil
	}

	for {
		// Check duration limit
		if maxDuration > 0 && time.Since(startTime) >= maxDuration {
			fmt.Fprintf(os.Stderr, "Max duration (%v) reached, stopping...\n", maxDuration)
			break
		}

		// Check size limit
		if maxSizeMB > 0 && bytesWritten >= maxSizeBytes {
			fmt.Fprintf(os.Stderr, "Max size (%d MB) reached, stopping...\n", maxSizeMB)
			break
		}

		// Check read timeout - if no data received for a while, assume connection is dead
		if time.Since(lastReadTime) > readTimeout {
			fmt.Fprintf(os.Stderr, "Read timeout (%v) - no data received, assuming connection closed\n", readTimeout)
			break
		}

		// Read RTP packet from track
		// Use ReadRTP to get the full RTP packet, then extract payload
		rtpPacket, _, readErr := track.ReadRTP()
		if readErr != nil {
			if readErr == io.EOF {
				fmt.Fprintf(os.Stderr, "Track ended (EOF)\n")
				break
			}
			// Check if it's a connection error
			if strings.Contains(readErr.Error(), "closed") || strings.Contains(readErr.Error(), "EOF") {
				fmt.Fprintf(os.Stderr, "Connection closed: %v\n", readErr)
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading track: %v\n", readErr)
			break
		}

		if rtpPacket == nil {
			continue
		}

		lastReadTime = time.Now() // Update last read time on successful read
		packetCount++

		// Extract RTP payload
		payload := rtpPacket.Payload
		if len(payload) < 1 {
			continue
		}

		// Parse RTP H.264 payload according to RFC 6184
		nalHeader := payload[0]
		nalType := nalHeader & 0x1F

		switch {
		case nalType >= 1 && nalType <= 23:
			// Single NAL unit (types 1-23)
			if err := writeNALUnit(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
				continue
			}
			// Clear any pending FU buffer
			fuBuffer = nil

		case nalType == 24:
			// STAP-A (Single-time aggregation packet)
			// Format: [STAP-A header] + [2 bytes size] + [NAL unit] + [2 bytes size] + [NAL unit] + ...
			offset := 1
			for offset < len(payload) {
				if offset+2 > len(payload) {
					break
				}
				nalSize := int(payload[offset])<<8 | int(payload[offset+1])
				offset += 2
				if offset+nalSize > len(payload) {
					break
				}
				nalData := payload[offset : offset+nalSize]
				if err := writeNALUnit(nalData); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing STAP-A NAL unit: %v\n", err)
					break
				}
				offset += nalSize
			}
			fuBuffer = nil

		case nalType == 28:
			// FU-A (Fragmentation unit)
			// Format: [FU indicator] + [FU header] + [FU payload]
			if len(payload) < 2 {
				continue
			}
			fuHeader := payload[1]
			start := (fuHeader & 0x80) != 0
			end := (fuHeader & 0x40) != 0
			actualNALType := fuHeader & 0x1F

			if start {
				// Start of fragmented NAL unit
				fuNALType = actualNALType
				fuBuffer = []byte{(nalHeader & 0xE0) | actualNALType} // Reconstruct NAL header
				fuBuffer = append(fuBuffer, payload[2:]...)
			} else {
				// Continuation or end
				if fuBuffer != nil && (fuHeader&0x1F) == fuNALType {
					fuBuffer = append(fuBuffer, payload[2:]...)
				} else {
					// Mismatch, discard
					fuBuffer = nil
					continue
				}
			}

			if end {
				// End of fragmented NAL unit - write complete NAL unit
				if fuBuffer != nil {
					if err := writeNALUnit(fuBuffer); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing FU-A NAL unit: %v\n", err)
					}
					fuBuffer = nil
				}
			}

		default:
			// Unknown or unsupported NAL type, skip
			fmt.Fprintf(os.Stderr, "Warning: Unsupported NAL type %d, skipping\n", nalType)
		}

		// Flush periodically to ensure data is written to disk
		if time.Since(lastFlushTime) > 1*time.Second {
			writer.Flush()
			file.Sync()
			elapsed := time.Since(startTime)
			sizeMB := float64(bytesWritten) / (1024 * 1024)
			fmt.Fprintf(os.Stderr, "Progress: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed.Round(time.Second))
			lastFlushTime = time.Now()
		}
	}

	// If there's an incomplete FU-A fragment, discard it (don't write incomplete NAL units)
	if fuBuffer != nil {
		fmt.Fprintf(os.Stderr, "Warning: Discarding incomplete FU-A fragment\n")
	}

	// Final flush - ensure all data is written to disk
	writer.Flush()
	file.Sync()
	elapsed := time.Since(startTime)
	sizeMB := float64(bytesWritten) / (1024 * 1024)
	fmt.Fprintf(os.Stderr, "Completed: %d packets, %.2f MB, %v elapsed\n", packetCount, sizeMB, elapsed)
	fmt.Fprintf(os.Stderr, "File flushed and synced to disk\n")
	fmt.Fprintf(os.Stderr, "You can now use FFmpeg to process this file:\n")
	fmt.Fprintf(os.Stderr, "  ffmpeg -fflags +genpts -r 30 -i %s -c:v copy received.mp4\n", filename)
}

// Read from stdin until we get a newline.
func readUntilNewline() (in string) {
	var err error

	r := bufio.NewReader(os.Stdin)
	for {
		in, err = r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			panic(err)
		}

		if in = strings.TrimSpace(in); len(in) > 0 {
			break
		}

		if err == io.EOF {
			break
		}
	}

	return
}

// JSON encode + base64 a SessionDescription.
func encode(obj *webrtc.SessionDescription) string {
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}

	return base64.StdEncoding.EncodeToString(b)
}

// Decode a base64 and unmarshal JSON into a SessionDescription.
func decode(in string, obj *webrtc.SessionDescription) {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		panic(err)
	}

	if err = json.Unmarshal(b, obj); err != nil {
		panic(err)
	}
}
