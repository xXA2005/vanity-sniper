package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valyala/fasthttp"
)

type Config struct {
	Webhook string `json:"webhook"`
	Token   string `json:"token"`
	GuildID []any  `json:"guild_id"`
}

var (
	config     Config
	fileMutex  sync.Mutex
	claimMutex sync.Mutex
	guildIndex = 0
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
}

func main() {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial("wss://gateway.discord.gg/?v=9&encoding=json", nil)
	if err != nil {
		fmt.Println("error connecting to ws:", err)
	}
	defer conn.Close()

	vanities := make(map[string]string)

	for {
		var data map[string]any
		err := conn.ReadJSON(&data)
		if err != nil {
			log.Println("error reading json:", err)
			break
		}
		opcode := int(data["op"].(float64))

		switch opcode {
		case 0: // dispatch
			nigger, ok := data["d"].(map[string]any)
			if ok {
				for key, val := range nigger {
					if key == "guilds" {
						for _, guild := range val.([]any) {
							vanity, ok := guild.(map[string]any)["vanity_url_code"].(string)
							if ok {
								vanities[guild.(map[string]any)["id"].(string)] = vanity
							}
						}
					}
				}
			}

			t := data["t"].(string)
			switch t {
			case "GUILD_UPDATE":
				if config.GuildID[guildIndex] == data["d"].(map[string]any)["guild_id"].(string) {
					continue
				}
				v, ok := vanities[fmt.Sprint(data["d"].(map[string]any)["guild_id"].(string))]
				if !ok {
					continue
				}
				if v != data["d"].(map[string]any)["vanity_url_code"].(string) {
					if !claimMutex.TryLock() {
						continue
					}
					start := time.Now()
					claimCode, resp := claim(v)
					end := time.Now()
					claimMutex.Unlock()

					switch claimCode {
					case 200:
						SendWhRaw(config.Webhook, map[string]any{
							"content": "",
							"tts":     false,
							"embeds": []map[string]any{
								{
									"description": fmt.Sprintf("**/%s Claimed Successfully**", v),
									"color":       2601760,
									"author": map[string]any{
										"name": "nigga",
									},
								},
							},
						})
						guildIndex++
						if guildIndex == len(config.GuildID) {
							SendWh(config.Webhook, "finished claiming all guilds add more guilds")
							os.Exit(0)
						}
					default:
						fmt.Printf("failed claim error %d with %s in %dms %s\n", claimCode, v, end.Sub(start).Milliseconds(), resp)
						SendWhRaw(config.Webhook, map[string]any{
							"content": "",
							"tts":     false,
							"embeds": []map[string]any{
								{
									"description": fmt.Sprintf("**/%s Missing**", v),
									"color":       11739168,
									"author": map[string]any{
										"name": "nigga",
									},
								},
							},
						})
					}
				}
			case "GUILD_DELETE":
				SendWh(config.Webhook, fmt.Sprintf("kicked from %v", data["d"].(map[string]any)["id"].(string)))
				SendWhRaw(config.Webhook, map[string]any{
					"content": "",
					"tts":     false,
					"embeds": []any{
						map[string]any{
							"description": fmt.Sprintf("**Server ID: %v**\n**Server vanity: %v**", data["d"].(map[string]any)["id"].(string), vanities[data["d"].(map[string]any)["id"].(string)]),
							"color":       11739168,
							"author": map[string]any{
								"name":     "OnlySniper[Removed]",
								"icon_url": "https://cdn.discordapp.com/attachments/1213086547973251152/1213097133318873138/1.png?ex=65f43bbe&is=65e1c6be&hm=ed35df2937b2a02c536727958b37c83df13819357965d85ef0256ccb322f4c47&",
							},
						},
					},
				})
			case "READY":
				fmt.Printf("connected as %s\n", data["d"].(map[string]any)["user"].(map[string]any)["username"].(string))
			}

		case 7:
			fmt.Println("reconnecting")
			payload := map[string]any{"op": 6, "d": nil}
			err := conn.WriteJSON(payload)
			if err != nil {
				log.Println("error sending resume payload:", err)
			}

		case 9:
			fmt.Println("invalid session")
		case 10:
			payload := map[string]any{
				"op": 2,
				"d": map[string]any{
					"token": config.Token,
					"properties": map[string]any{
						"os":      "linux",
						"browser": "firefox",
						"device":  "pc",
					},
				},
			}
			err := conn.WriteJSON(payload)
			if err != nil {
				log.Println("error sending paylod:", err)
			}
			heartbeat := data["d"].(map[string]any)["heartbeat_interval"].(float64)
			go func() {
				for {
					time.Sleep(time.Duration(heartbeat) * time.Millisecond)
					payload := map[string]any{"op": 1, "d": nil}
					err := conn.WriteJSON(payload)
					if err != nil {
						log.Println("error sending heartbeat:", err)
						break
					}
				}
			}()
		}
	}
}

func SendWh(wh, msg string) {
	payloadMap := map[string]string{"content": msg}
	b, _ := json.Marshal(payloadMap)
	http.Post(wh, "application/json", bytes.NewReader(b))
}

func SendWhRaw(wh string, payloadMap map[string]any) {
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

func claim(vanity string) (int, string) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(fmt.Sprintf("https://discord.com/api/v9/guilds/%s/vanity-url", config.GuildID[guildIndex]))
	req.Header.SetMethod(fasthttp.MethodPatch)
	req.Header.SetContentType("application/json")
	req.Header.Add("authorization", config.Token)
	req.Header.Add("accept", "*/*")
	payload := map[string]string{"code": vanity}
	b, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(err)
	}
	client := &fasthttp.Client{TLSConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}
	req.SetBody(b)
	err = client.Do(req, resp)
	if err != nil {
		fmt.Println(err)
	}

	return resp.StatusCode(), string(resp.Body())
}
