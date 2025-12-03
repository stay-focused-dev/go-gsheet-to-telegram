package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	MaxChannelDuration = 24 * time.Hour
	StateFile          = ".drive-channels.json"
)

type DriveWatcher struct {
	service        *drive.Service
	webhookURL     string
	activeChannels map[string]*ChannelInfo
	mu             sync.Mutex
}

type ChannelInfo struct {
	Id         string    `json:"id"`
	ResourceId string    `json:"resource_id"`
	FileId     string    `json:"file_id"`
	Expiration time.Time `json:"expiration"`
}

type ChannelState struct {
	Channels map[string]*ChannelInfo `json:"channels"`
}

func NewDriveWatcher(credentials []byte, webhookURL string) (*DriveWatcher, error) {
	ctx := context.Background()

	config, err := google.JWTConfigFromJSON(credentials, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	client := config.Client(ctx)

	service, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}

	watcher := &DriveWatcher{
		service:        service,
		webhookURL:     webhookURL,
		activeChannels: make(map[string]*ChannelInfo),
	}

	// Load previous state if exists
	if err := watcher.loadState(); err != nil {
		log.Printf("Warning: failed to load previous state: %v", err)
	} else if len(watcher.activeChannels) > 0 {
		log.Printf("Loaded %d channel(s) from previous state", len(watcher.activeChannels))
	}

	return watcher, nil
}

func (dw *DriveWatcher) loadState() error {
	data, err := os.ReadFile(StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state ChannelState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}

	dw.mu.Lock()
	dw.activeChannels = state.Channels
	if dw.activeChannels == nil {
		dw.activeChannels = make(map[string]*ChannelInfo)
	}
	dw.mu.Unlock()

	return nil
}

func (dw *DriveWatcher) saveState() error {
	dw.mu.Lock()
	defer dw.mu.Unlock()

	// If no channels, remove the state file
	if len(dw.activeChannels) == 0 {
		os.Remove(StateFile)
		return nil
	}

	state := ChannelState{
		Channels: dw.activeChannels,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return os.WriteFile(StateFile, data, 0644)
}

func (dw *DriveWatcher) cleanupOldChannels(fileId string) {
	log.Printf("Cleaning up old channels for file %s...", fileId)

	dw.mu.Lock()
	channelsToStop := make([]*ChannelInfo, 0)
	for _, info := range dw.activeChannels {
		if info.FileId == fileId {
			channelsToStop = append(channelsToStop, info)
		}
	}
	dw.mu.Unlock()

	stoppedCount := 0
	for _, info := range channelsToStop {
		log.Printf("Stopping old channel: %s (expires: %s)", info.Id, info.Expiration)

		channel := &drive.Channel{
			Id:         info.Id,
			ResourceId: info.ResourceId,
		}

		err := dw.service.Channels.Stop(channel).Do()
		if err != nil {
			log.Printf("Warning: failed to stop channel %s: %v", info.Id, err)
		} else {
			log.Printf("✓ Stopped old channel: %s", info.Id)
			stoppedCount++
		}

		dw.mu.Lock()
		delete(dw.activeChannels, info.Id)
		dw.mu.Unlock()
	}

	if stoppedCount > 0 {
		dw.saveState()
		log.Printf("Cleaned up %d old channel(s)", stoppedCount)
	} else {
		log.Println("No old channels to clean up")
	}
}

func (dw *DriveWatcher) cleanupExpiredChannels() {
	log.Println("Cleaning up expired channels...")

	now := time.Now()
	expiredCount := 0

	dw.mu.Lock()
	for id, info := range dw.activeChannels {
		if info.Expiration.Before(now) {
			log.Printf("Removing expired channel: %s (expired: %s)", id, info.Expiration)
			delete(dw.activeChannels, id)
			expiredCount++
		}
	}
	dw.mu.Unlock()

	if expiredCount > 0 {
		dw.saveState()
		log.Printf("Removed %d expired channel(s)", expiredCount)
	}
}

func (dw *DriveWatcher) WatchFile(fileId string) (*ChannelInfo, error) {
	// Clean up old channels before creating a new one
	dw.cleanupOldChannels(fileId)
	dw.cleanupExpiredChannels()

	return dw.createWatch(fileId)
}

// createWatch creates a new watch without cleanup (used internally)
func (dw *DriveWatcher) createWatch(fileId string) (*ChannelInfo, error) {
	channelId := uuid.New().String()
	expiration := time.Now().Add(MaxChannelDuration).UnixMilli()

	channel := &drive.Channel{
		Id:         channelId,
		Type:       "web_hook",
		Address:    dw.webhookURL,
		Expiration: expiration,
		Token:      "secret-" + channelId, // Add token for security verification
	}

	log.Printf("Creating watch for file %s with channel %s", fileId, channelId)

	result, err := dw.service.Files.Watch(fileId, channel).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to watch file: %w", err)
	}

	info := &ChannelInfo{
		Id:         result.Id,
		ResourceId: result.ResourceId,
		FileId:     fileId,
		Expiration: time.UnixMilli(result.Expiration),
	}

	dw.mu.Lock()
	dw.activeChannels[result.Id] = info
	dw.mu.Unlock()

	if err := dw.saveState(); err != nil {
		log.Printf("Warning: failed to save state: %v", err)
	}

	log.Printf("Watch created: Channel=%s, Resource=%s, Expires=%s",
		info.Id, info.ResourceId, info.Expiration)
	return info, nil
}

func (dw *DriveWatcher) StopWatch(channelId string) error {
	dw.mu.Lock()
	info, exists := dw.activeChannels[channelId]
	dw.mu.Unlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelId)
	}

	stopChannel := &drive.Channel{
		Id:         info.Id,
		ResourceId: info.ResourceId,
	}

	err := dw.service.Channels.Stop(stopChannel).Do()
	if err != nil {
		return fmt.Errorf("failed to stop channel: %w", err)
	}

	dw.mu.Lock()
	delete(dw.activeChannels, channelId)
	dw.mu.Unlock()

	if err := dw.saveState(); err != nil {
		log.Printf("Warning: failed to save state: %v", err)
	}

	log.Printf("Channel %s stopped", channelId)
	return nil
}

func (dw *DriveWatcher) RenewWatch(fileId, oldChannelId string) (*ChannelInfo, error) {
	if err := dw.StopWatch(oldChannelId); err != nil {
		log.Printf("Warning: failed to stop old channel: %v", err)
	}

	// Use createWatch to avoid double cleanup
	return dw.createWatch(fileId)
}

func (dw *DriveWatcher) StartChannelRenewer(fileId string) {
	ticker := time.NewTicker(20 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		dw.mu.Lock()
		channelsToRenew := make([]string, 0)
		for channelId, info := range dw.activeChannels {
			if time.Until(info.Expiration) < 4*time.Hour {
				channelsToRenew = append(channelsToRenew, channelId)
			}
		}
		dw.mu.Unlock()

		for _, channelId := range channelsToRenew {
			log.Printf("Renewing channel %s (expires soon)", channelId)

			newInfo, err := dw.RenewWatch(fileId, channelId)
			if err != nil {
				log.Printf("Failed to renew channel %s: %v", channelId, err)
			} else {
				log.Printf("Channel renewed: %s -> %s", channelId, newInfo.Id)
			}
		}
	}
}

func (dw *DriveWatcher) StopAllChannels() {
	log.Println("Stopping all active channels...")

	dw.mu.Lock()
	channelsToStop := make([]*ChannelInfo, 0, len(dw.activeChannels))
	for _, info := range dw.activeChannels {
		channelsToStop = append(channelsToStop, info)
	}
	dw.mu.Unlock()

	for _, info := range channelsToStop {
		if err := dw.StopWatch(info.Id); err != nil {
			log.Printf("Failed to stop channel %s: %v", info.Id, err)
		}
	}

	log.Println("All channels stopped")
}

type DriveNotification struct {
	ChannelId     string `header:"X-Goog-Channel-ID"`
	ResourceId    string `header:"X-Goog-Resource-ID"`
	ResourceState string `header:"X-Goog-Resource-State"`
	ResourceURI   string `header:"X-Goog-Resource-URI"`
	MessageNumber string `header:"X-Goog-Message-Number"`
	Expiration    string `header:"X-Goog-Channel-Expiration"`
}

func (dw *DriveWatcher) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	notification := DriveNotification{
		ChannelId:     r.Header.Get("X-Goog-Channel-ID"),
		ResourceId:    r.Header.Get("X-Goog-Resource-ID"),
		ResourceState: r.Header.Get("X-Goog-Resource-State"),
		ResourceURI:   r.Header.Get("X-Goog-Resource-URI"),
		MessageNumber: r.Header.Get("X-Goog-Message-Number"),
		Expiration:    r.Header.Get("X-Goog-Channel-Expiration"),
	}

	// Verify this is our channel
	dw.mu.Lock()
	_, isOurs := dw.activeChannels[notification.ChannelId]
	dw.mu.Unlock()

	if !isOurs {
		log.Printf("Ignoring notification from unknown channel: %s", notification.ChannelId)
		w.WriteHeader(http.StatusOK)
		return
	}

	log.Printf("Received notification: Channel=%s, State=%s, Resource=%s, Msg#=%s",
		notification.ChannelId,
		notification.ResourceState,
		notification.ResourceId,
		notification.MessageNumber,
	)

	switch notification.ResourceState {
	case "sync":
		log.Printf("Channel %s synchronized", notification.ChannelId)
	case "change":
		log.Printf("File changed! Channel=%s, Resource=%s",
			notification.ChannelId, notification.ResourceId,
		)
		go dw.handleFileChange(notification)
	case "update":
		log.Printf("File metadata updated: %s", notification.ResourceId)
		go dw.handleFileChange(notification)
	default:
		log.Printf("Unknown state: %s", notification.ResourceState)
	}

	w.WriteHeader(http.StatusOK)
}

func (dw *DriveWatcher) handleFileChange(notification DriveNotification) {
	log.Printf("Processing file change for resource %s", notification.ResourceId)

	// TODO: Implement your business logic here:
	// 1. Fetch the updated spreadsheet content
	// 2. Parse tasks and schedules
	// 3. Update notification queue
	// 4. Send notifications to Telegram/Email
}

func usage() {
	program := filepath.Base(os.Args[0])
	fmt.Printf("Usage: %s -creds CREDS_FILE -webhook WEBHOOK_URL -sheet GSHEET_ID [-port WEBHOOK_PORT]\n", program)
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -creds string")
	fmt.Println("        Path to JSON credentials file for Google Drive API (required)")
	fmt.Println("  -webhook string")
	fmt.Println("        Webhook URL for receiving Google Drive notifications (required)")
	fmt.Println("  -sheet string")
	fmt.Println("        Google Sheet ID to watch (required)")
	fmt.Println("  -port int")
	fmt.Println("        Port for webhook server (default: 8080)")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Printf("  %s -creds ./credentials.json -webhook https://example.com/webhook -sheet 1W0w...mWXE\n", program)
	os.Exit(1)
}

func main() {
	credentialsFile := flag.String("creds", "", "file with JSON credentials to GDrive API")
	webhookURL := flag.String("webhook", "", "webhook URL for GDrive")
	sheetId := flag.String("sheet", "", "sheet id")
	port := flag.Int("port", 8080, "port for webhook URL")

	flag.Parse()

	if *credentialsFile == "" || *webhookURL == "" || *sheetId == "" {
		usage()
	}

	credentials, err := os.ReadFile(*credentialsFile)
	if err != nil {
		log.Fatalf("Unable to read credentials file: %v", err)
	}

	watcher, err := NewDriveWatcher(credentials, *webhookURL)
	if err != nil {
		log.Fatal(err)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("\nReceived signal: %v", sig)
		log.Println("Shutting down gracefully...")
		watcher.StopAllChannels()
		os.Exit(0)
	}()

	// Create watch (this will cleanup old channels automatically)
	_, err = watcher.WatchFile(*sheetId)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("✓ Watching sheet: %s", *sheetId)

	// Start channel renewer in background
	go watcher.StartChannelRenewer(*sheetId)

	// Setup webhook handler
	http.HandleFunc("/drive-webhook", watcher.WebhookHandler)

	// Start HTTP server
	hostport := fmt.Sprintf(":%d", *port)
	log.Printf("Starting webhook server on %s", hostport)
	log.Println("Press Ctrl+C to stop")

	if err := http.ListenAndServe(hostport, nil); err != nil {
		log.Fatal(err)
	}
}
