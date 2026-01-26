// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// server is a WebRTC server that sends video from a local file using FFmpeg
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asticode/go-astiav"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

var (
	inputFormatContext   *astiav.FormatContext
	decodeCodecContext   *astiav.CodecContext
	decodePacket         *astiav.Packet
	decodeFrame          *astiav.Frame
	videoStream          *astiav.Stream
	audioStream          *astiav.Stream
	softwareScaleContext *astiav.SoftwareScaleContext
	scaledFrame          *astiav.Frame
	encodeCodecContext   *astiav.CodecContext
	encodePacket         *astiav.Packet
	pts                  int64
	err                  error
)

func main() {
	videoFile := flag.String("video", "", "Video file path (e.g., Ultra.mp4)")
	localIP := flag.String("ip", "", "Local IP address for WebRTC (e.g., 192.168.100.1). If not specified, auto-detect")
	offerFile := flag.String("offer-file", "", "Path to file to write offer (optional, if not specified, write to stdout)")
	answerFile := flag.String("answer-file", "", "Path to file containing answer (optional, if not specified, read from stdin)")
	loop := flag.Bool("loop", false, "Loop video playback (default: false, play once)")
	flag.Parse()

	if *videoFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -video parameter is required\n")
		os.Exit(1)
	}

	// Check if video file exists
	if _, err := os.Stat(*videoFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: video file not found: %s\n", *videoFile)
		os.Exit(1)
	}

	// Get absolute path for the video file
	absPath, err := filepath.Abs(*videoFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get absolute path: %v\n", err)
		os.Exit(1)
	}

	// Register all devices
	astiav.RegisterAllDevices()

	// Everything below is the Pion WebRTC API! Thanks for using it ❤️.

	// Create SettingEngine to configure port range and ICE timeouts
	settingEngine := webrtc.SettingEngine{}
	// Set fixed UDP port range for easier testing (use same range for both)
	if err := settingEngine.SetEphemeralUDPPortRange(50000, 50100); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to set port range: %v\n", err)
	}
	// Increase ICE timeouts for localhost testing (default might be too short)
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
	// For localhost testing, we don't need STUN servers - host candidates are sufficient
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// Empty - rely only on host candidates for localhost communication
		},
	}

	if *localIP != "" {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (LAN mode, IP: %s, fixed port range 50000-50100)...\n", *localIP)
	} else {
		fmt.Fprintf(os.Stderr, "Starting ICE gathering (localhost mode, no STUN, fixed port range 50000-50100)...\n")
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

	// Create context to wait for ICE connection
	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

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
		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "ICE connection established!\n")
			iceConnectedCtxCancel()
		} else if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: ICE connection failed!\n")
		}
	})

	// Set the handler for Peer connection state
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Fprintf(os.Stderr, "Peer Connection State: %s\n", s.String())
		if s == webrtc.PeerConnectionStateConnected {
			fmt.Fprintf(os.Stderr, "Peer connection established!\n")
		} else if s == webrtc.PeerConnectionStateFailed {
			fmt.Fprintf(os.Stderr, "ERROR: Peer connection failed!\n")
		}
	})

	// Create video track (H264)
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/h264"}, "video", "pion")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(videoTrack)
	if err != nil {
		panic(err)
	}

	// Create audio track (Opus) - optional
	opusTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}
	_, err = peerConnection.AddTrack(opusTrack)
	if err != nil {
		panic(err)
	}

	// Create an offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	fmt.Fprintf(os.Stderr, "Waiting for ICE gathering to complete...\n")
	<-gatherComplete
	fmt.Fprintf(os.Stderr, "ICE gathering completed\n")

	// Output the offer in base64 to stdout or file
	offerStr := encode(peerConnection.LocalDescription())
	if *offerFile != "" {
		err := os.WriteFile(*offerFile, []byte(offerStr+"\n"), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing offer to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Offer written to file: %s (%d bytes)\n", *offerFile, len(offerStr))
	} else {
		// Write directly to stdout with newline, then flush
		os.Stdout.WriteString(offerStr + "\n")
		os.Stdout.Sync()
		fmt.Fprintf(os.Stderr, "Offer written to stdout (%d bytes)\n", len(offerStr))
	}

	// Wait for the answer from stdin or file
	fmt.Fprintf(os.Stderr, "Waiting for answer from client...\n")
	answer := webrtc.SessionDescription{}
	var answerStr string
	if *answerFile != "" {
		fmt.Fprintf(os.Stderr, "Reading answer from file: %s\n", *answerFile)
		answerStr = readFromFile(*answerFile)
	} else {
		answerStr = readUntilNewline()
	}
	if answerStr == "" {
		fmt.Fprintf(os.Stderr, "Error: Empty answer received\n")
		os.Exit(1)
	}
	// Validate that answerStr looks like base64
	if len(answerStr) < 100 {
		fmt.Fprintf(os.Stderr, "Error: Answer too short (%d chars), expected base64 string\n", len(answerStr))
		os.Exit(1)
	}
	decode(answerStr, &answer)
	fmt.Fprintf(os.Stderr, "Answer received, setting remote description...\n")

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(answer)
	if err != nil {
		panic(fmt.Sprintf("Failed to set remote description: %v", err))
	}

	fmt.Fprintf(os.Stderr, "Waiting for ICE connection to establish...\n")
	// Wait for ICE connection to be established before starting video streaming
	// Add timeout to avoid waiting forever
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	select {
	case <-iceConnectedCtx.Done():
		fmt.Fprintf(os.Stderr, "ICE connection established, starting video streaming...\n")
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "WARNING: ICE connection timeout, starting video streaming anyway...\n")
	}

	// Initialize video source from file
	initVideoSource(absPath)
	defer freeVideoCoding()

	// Create channel for video completion signal
	videoDone := make(chan bool, 1)

	// Start pushing video frames
	go writeVideoToTrack(videoTrack, *loop, videoDone)

	// Wait for video completion or connection close
	select {
	case <-videoDone:
		fmt.Fprintf(os.Stderr, "Video streaming completed, closing connection...\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	case <-time.After(24 * time.Hour): // Safety timeout (should never trigger)
		fmt.Fprintf(os.Stderr, "Timeout waiting for video completion\n")
		if err := peerConnection.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing peer connection: %v\n", err)
		}
	}
}

func initVideoSource(videoPath string) {
	if inputFormatContext = astiav.AllocFormatContext(); inputFormatContext == nil {
		panic("Failed to AllocFormatContext")
	}

	// Open input file
	if err = inputFormatContext.OpenInput(videoPath, nil, nil); err != nil {
		panic(fmt.Sprintf("Failed to open input file: %v", err))
	}

	// Find stream info
	if err = inputFormatContext.FindStreamInfo(nil); err != nil {
		panic(fmt.Sprintf("Failed to find stream info: %v", err))
	}

	// Find video stream
	for _, stream := range inputFormatContext.Streams() {
		if stream.CodecParameters().CodecType() == astiav.MediaTypeVideo {
			videoStream = stream
			break
		}
		if stream.CodecParameters().CodecType() == astiav.MediaTypeAudio {
			audioStream = stream
		}
	}

	if videoStream == nil {
		panic("No video stream found in file")
	}

	// Get decoder
	decodeCodec := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decodeCodec == nil {
		panic("FindDecoder returned nil")
	}

	if decodeCodecContext = astiav.AllocCodecContext(decodeCodec); decodeCodecContext == nil {
		panic("Failed to AllocCodecContext")
	}

	if err = videoStream.CodecParameters().ToCodecContext(decodeCodecContext); err != nil {
		panic(fmt.Sprintf("Failed to copy codec parameters: %v", err))
	}

	decodeCodecContext.SetFramerate(inputFormatContext.GuessFrameRate(videoStream, nil))

	if err = decodeCodecContext.Open(decodeCodec, nil); err != nil {
		panic(fmt.Sprintf("Failed to open decoder: %v", err))
	}

	decodePacket = astiav.AllocPacket()
	decodeFrame = astiav.AllocFrame()

	// Initialize encoder (will be set up after we know the frame size)
}

func initVideoEncoding() {
	if encodeCodecContext != nil {
		return
	}

	h264Encoder := astiav.FindEncoder(astiav.CodecIDH264)
	if h264Encoder == nil {
		panic("No H264 Encoder Found")
	}

	if encodeCodecContext = astiav.AllocCodecContext(h264Encoder); encodeCodecContext == nil {
		panic("Failed to AllocCodecContext Encoder")
	}

	encodeCodecContext.SetPixelFormat(astiav.PixelFormatYuv420P)
	encodeCodecContext.SetSampleAspectRatio(decodeCodecContext.SampleAspectRatio())
	encodeCodecContext.SetTimeBase(astiav.NewRational(1, 30))
	encodeCodecContext.SetWidth(decodeCodecContext.Width())
	encodeCodecContext.SetHeight(decodeCodecContext.Height())

	encodeCodecContextDictionary := astiav.NewDictionary()
	if err = encodeCodecContextDictionary.Set("preset", "ultrafast", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}
	if err = encodeCodecContextDictionary.Set("tune", "zerolatency", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}
	if err = encodeCodecContextDictionary.Set("bf", "0", astiav.NewDictionaryFlags()); err != nil {
		panic(err)
	}

	if err = encodeCodecContext.Open(h264Encoder, encodeCodecContextDictionary); err != nil {
		panic(fmt.Sprintf("Failed to open encoder: %v", err))
	}

	softwareScaleContext, err = astiav.CreateSoftwareScaleContext(
		decodeCodecContext.Width(),
		decodeCodecContext.Height(),
		decodeCodecContext.PixelFormat(),
		decodeCodecContext.Width(),
		decodeCodecContext.Height(),
		astiav.PixelFormatYuv420P,
		astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create scale context: %v", err))
	}

	scaledFrame = astiav.AllocFrame()
}

func writeVideoToTrack(track *webrtc.TrackLocalStaticSample, loopVideo bool, done chan<- bool) {
	frameRate := videoStream.AvgFrameRate()
	if frameRate.Num() == 0 {
		frameRate = astiav.NewRational(30, 1)
	}
	h264FrameDuration := time.Duration(float64(time.Second) * float64(frameRate.Den()) / float64(frameRate.Num()))

	ticker := time.NewTicker(h264FrameDuration)
	defer ticker.Stop()

	for range ticker.C {
		decodePacket.Unref()

		// Read frame from file
		if err = inputFormatContext.ReadFrame(decodePacket); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				if loopVideo {
					// Loop the video - seek to beginning
					if err = inputFormatContext.SeekFrame(0, 0, astiav.NewSeekFlags(astiav.SeekFlagFrame)); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to seek to beginning: %v\n", err)
						break
					}
					pts = 0
					fmt.Fprintf(os.Stderr, "Video looped, restarting from beginning...\n")
					continue
				} else {
					// Play once, stop when EOF
					fmt.Fprintf(os.Stderr, "Video playback completed (EOF reached)\n")
					// Send completion signal
					select {
					case done <- true:
					default:
					}
					break
				}
			}
			fmt.Fprintf(os.Stderr, "Error reading frame: %v\n", err)
			continue
		}

		// Only process video packets
		if decodePacket.StreamIndex() != videoStream.Index() {
			continue
		}

		decodePacket.RescaleTs(videoStream.TimeBase(), decodeCodecContext.TimeBase())

		// Send the packet to decoder
		if err = decodeCodecContext.SendPacket(decodePacket); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending packet to decoder: %v\n", err)
			continue
		}

		for {
			// Read Decoded Frame
			if err = decodeCodecContext.ReceiveFrame(decodeFrame); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					break
				}
				fmt.Fprintf(os.Stderr, "Error receiving frame: %v\n", err)
				break
			}

			// Init the Scaling+Encoding. Can't be started until we know info on input video
			initVideoEncoding()

			// Scale the video
			if err = softwareScaleContext.ScaleFrame(decodeFrame, scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error scaling frame: %v\n", err)
				continue
			}

			// Set PTS
			pts++
			scaledFrame.SetPts(pts)

			// Encode the frame
			if err = encodeCodecContext.SendFrame(scaledFrame); err != nil {
				fmt.Fprintf(os.Stderr, "Error sending frame to encoder: %v\n", err)
				continue
			}

			for {
				// Read encoded packets
				encodePacket = astiav.AllocPacket()
				if err = encodeCodecContext.ReceivePacket(encodePacket); err != nil {
					if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
						encodePacket.Free()
						break
					}
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error receiving packet: %v\n", err)
					break
				}

				// Write H264 to track
				if err = track.WriteSample(media.Sample{Data: encodePacket.Data(), Duration: h264FrameDuration}); err != nil {
					encodePacket.Free()
					fmt.Fprintf(os.Stderr, "Error writing sample: %v\n", err)
					continue
				}

				encodePacket.Free()
			}
		}
	}
}

func freeVideoCoding() {
	if inputFormatContext != nil {
		inputFormatContext.CloseInput()
		inputFormatContext.Free()
	}

	if decodeCodecContext != nil {
		decodeCodecContext.Free()
	}
	if decodePacket != nil {
		decodePacket.Free()
	}
	if decodeFrame != nil {
		decodeFrame.Free()
	}

	if scaledFrame != nil {
		scaledFrame.Free()
	}
	if softwareScaleContext != nil {
		softwareScaleContext.Free()
	}
	if encodeCodecContext != nil {
		encodeCodecContext.Free()
	}
	if encodePacket != nil {
		encodePacket.Free()
	}
}

// Read from stdin until we get a newline.
// This function blocks until a line is read, which is appropriate for interactive terminal input.
func readUntilNewline() (in string) {
	r := bufio.NewReader(os.Stdin)

	// For interactive terminal input, we can simply read a line directly
	// This will block until the user pastes the answer and presses Enter
	fmt.Fprintf(os.Stderr, "Paste the answer here and press Enter: ")

	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
		return ""
	}

	in = strings.TrimSpace(line)
	if len(in) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: Empty line received, please paste the answer again\n")
		return ""
	}

	return in
}

// Read from file, waiting for file to be created and have content.
// This function polls the file periodically until it has content or timeout.
func readFromFile(filePath string) (in string) {
	deadline := time.Now().Add(60 * time.Second)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		// Check if file exists and has content
		data, err := os.ReadFile(filePath)
		if err == nil && len(data) > 0 {
			in = strings.TrimSpace(string(data))
			if len(in) > 0 {
				fmt.Fprintf(os.Stderr, "Answer read from file (%d bytes)\n", len(in))
				return in
			}
		}

		// Wait before next check
		time.Sleep(pollInterval)
		fmt.Fprintf(os.Stderr, "Waiting for answer file... (timeout in %v)\n", deadline.Sub(time.Now()).Round(time.Second))
	}

	fmt.Fprintf(os.Stderr, "Error: Timeout waiting for answer file: %s\n", filePath)
	return ""
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
