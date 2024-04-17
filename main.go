package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

type Config struct {
	Retries int    `json:"retries"`
	Threads int    `json:"threads"`
	Proxy   string `json:"proxy"`
	Webhook string `json:"webhook"`
	Token   string `json:"token"`
	GuildID []any  `json:"guild_id"`
}

var (
	config   Config
	vanities []string

	fileMutex  sync.Mutex
	claimMutex sync.Mutex
	guildIndex = 0
	hclient    = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}}

	attempts  = 0
	ka        = 0
	ratelimit = 0
	blocked   = 0
)

func init() {
	confBytes, err := os.ReadFile("./config.json")
	if err != nil {
		fmt.Println("failed to read config.json", err.Error())
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}

	err = json.Unmarshal(confBytes, &config)
	if err != nil {
		fmt.Println("failed to unmarshal config.json", err.Error())
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}

	vanBytes, err := os.ReadFile("./vanities.txt")
	if err != nil {
		fmt.Println("failed to read vanities.txt", err.Error())
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}
	vanities = strings.Split(strings.Replace(string(vanBytes), "\r", "", -1), "\n")
}

func keepAlive() {
	for {
		hclient.Get("https://discord.com/api/v9/gateway")
		ka++
		time.Sleep(5 * time.Second)
	}
}

func main() {
	go keepAlive()
	go updateTitle()
	vanityChan := make(chan string)
	for i := 0; i < config.Threads; i++ {
		go thread(vanityChan)
	}

	for {
		for _, vanity := range vanities {
			vanityChan <- vanity
		}
	}
}

func thread(channel chan string) {
	for {
		vanity := <-channel
		code := check(vanity)
		switch code {
		case 200: // unavaialbe
			attempts++
			fmt.Printf("%s[ Checking ] -> /%s%s\n", "\033[0;31m", vanity, "\033[0m")
		case 429:
			ratelimit++
		case 404: // avaialbe
			if !claimMutex.TryLock() {
				continue
			}
			start := time.Now()
			claimCode, resp := claim(vanity)
			end := time.Now()
			fmt.Printf("%s[ Claimed ] -> /%s%s\n", "\033[0;32m", vanity, "\033[0m")
			claimMutex.Unlock()
			switch claimCode {
			case 200: // claimed
				// SendWh(config.Webhook, fmt.Sprintf("claimed %s in %dms", vanity, end.Sub(start).Milliseconds()))
				SendWhRaw(config.Webhook, map[string]any{
					"content": "",
					"tts":     false,
					"embeds": []map[string]any{
						{
							"description": fmt.Sprintf("**/%s Claimed Successfully in %dms**", vanity, end.Sub(start).Milliseconds()),
							"color":       2601760,
							"author": map[string]any{
								"name":     "Sniper",
								"icon_url": "https://cdn.discordapp.com/attachments/1213086547973251152/1213097133318873138/1.png?ex=65f43bbe&is=65e1c6be&hm=ed35df2937b2a02c536727958b37c83df13819357965d85ef0256ccb322f4c47&",
							},
						},
					},
				})
				guildIndex++
				if guildIndex == len(config.GuildID) {
					SendWh(config.Webhook, "finished claiming all guilds add more guilds")
					os.Exit(0)
				}
			case 400: // expired
				n := time.Now()
				f, err := os.ReadFile("./expired.txt")
				if err != nil {
					fmt.Println("cant save expired vanity", vanity, err)
					break
				}
				if !strings.Contains(string(f), vanity) {
					removeVanity(vanity)
					SendWhRaw(config.Webhook, map[string]any{
						"content": "",
						"tts":     false,
						"embeds": []any{
							map[string]any{
								"description": fmt.Sprintf("**Vanity: /%s**\n**Expired at: %v**\n**Available at: %v**", vanity, n.Format("2006/01/02 | 03:04:05 pm"), n.Add(time.Hour*24*29).Format("2006/01/02 | 03:04:05 pm")),
								"color":       16777215,
								"author": map[string]string{
									"name":     "Monitor",
									"icon_url": "https://cdn.discordapp.com/attachments/1213086547973251152/1213097133318873138/1.png?ex=65f43bbe&is=65e1c6be&hm=ed35df2937b2a02c536727958b37c83df13819357965d85ef0256ccb322f4c47&",
								},
							},
						},
					})
					// SendWh(config.Webhook, fmt.Sprintf("%s [Expired: %s] -> [Available: %s]", vanity, n.Format(time.Layout), n.Add(time.Hour*24*29).Format(time.Layout)))
					err = WriteToFile("./expired.txt", fmt.Sprintf("%s|%d", vanity, n.UnixMilli()))
					if err != nil {
						fmt.Println("expired vanity but cant save", vanity, err.Error())
					}
				}
			default:
				blocked++
				fmt.Printf("unknown claim error %d in %dms %s\n", claimCode, end.Sub(start).Milliseconds(), resp)
			}
		default:
			fmt.Println("unknown check error", code)
		}
	}
}

func check(vanity string) int {
	for i := 0; i < config.Retries; i++ {
		client := &fasthttp.Client{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
			},
			Dial: fasthttpproxy.FasthttpHTTPDialer(config.Proxy),
		}
		code, _, err := client.Get(nil, fmt.Sprintf("https://ptb.discord.com/api/v9/invites/%s", vanity))
		if err != nil {
			fmt.Println("failed to send request", err.Error())
			continue
		}
		return code
	}
	return 0
}

func claim(vanity string) (int, string) {
	payload := map[string]string{"code": vanity}
	b, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(err)
	}
	req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("https://discord.com/api/v9/guilds/%s/vanity-url", config.GuildID[guildIndex]), bytes.NewReader(b))
	if err != nil {
		fmt.Println(err)
	}
	req.Header = http.Header{
		"authorization": {config.Token},
		"accept":        {"*/*"},
		"content-type":  {"application/json"},
	}

	resp, err := hclient.Do(req)
	if err != nil {
		fmt.Println(err)
	}
	b, err = io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
	}

	return resp.StatusCode, string(b)
}

func SendWh(wh, msg string) {
	payloadMap := map[string]string{"content": msg}
	b, _ := json.Marshal(payloadMap)
	http.Post(wh, "application/json", bytes.NewReader(b))
}

func WriteToFile(filePath, content string) error {
	fileMutex.Lock()
	defer fileMutex.Unlock()
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if fileInfo.Size() > 0 {
		content = "\n" + content
	}

	_, err = file.WriteString(content)
	if err != nil {
		return err
	}

	return nil
}

func SendWhRaw(wh string, payloadMap map[string]any) {
	b, _ := json.Marshal(payloadMap)
	http.Post(wh, "application/json", bytes.NewReader(b))
}

func updateTitle() {
	start := time.Now()
	for {
		title := fmt.Sprintf("Ratelimit: %d | Blocked: %d | Attempts: %d | rs: %.2f | keep alive: %d", ratelimit, blocked, attempts, float64(attempts)/time.Since(start).Seconds(), ka)
		p, _ := syscall.UTF16PtrFromString(title)
		syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(p)))
		time.Sleep(500 * time.Millisecond)
	}
}

func removeVanity(vanity string) {
	index := -1
	for i, val := range vanities {
		if val == vanity {
			index = i
			break
		}
	}

	if index == -1 {
		return
	}

	vanities = append(vanities[:index], vanities[index+1:]...)
}
