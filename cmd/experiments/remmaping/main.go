package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"text/template"
)

type Media struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Caption string `json:"caption"`
}

type Body struct {
	MsgID     string `json:"msg_id"`
	To        string `json:"to"`
	From      string `json:"from"`
	Label     string `json:"label"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	ChannelID string `json:"channel_id"`
	Media     Media  `json:"media"`
}

func main() {
	inputJSON := `{
		"msg_id": "1234567890",
		"to": "user123@email.com",
		"from": "user456@email.com",
		"label": "Hello, how are you?",
		"type": "msg",
		"channel_id": "1234567890",
		"media": {
			"type": "image",
			"url": "https://example.com/image.jpg",
			"caption": "This is a caption"
		},
		"another_attr": {
		  "attr1": "value1"
		}
	}`

	mappingFile, err := os.ReadFile("./mapping_configs/mapping_config.json")
	if err != nil {
		log.Fatalf("Error on read mapping: %v", err)
	}

	var inputMap map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &inputMap); err != nil {
		log.Fatalf("Error on deserialize: %v", err)
	}

	tmpl, err := template.New("mapping").Parse(string(mappingFile))
	if err != nil {
		log.Fatalf("Error on analize template: %v", err)
	}

	var outputBuffer bytes.Buffer
	if err := tmpl.Execute(&outputBuffer, inputMap); err != nil {
		log.Fatalf("Error on execute template: %v", err)
	}

	var newBody map[string]interface{}
	if err := json.Unmarshal(outputBuffer.Bytes(), &newBody); err != nil {
		log.Fatalf("Error to deserialize output JSON: %v", err)
	}

	prettyJSON, err := json.MarshalIndent(newBody, "", "  ")
	if err != nil {
		log.Fatalf("formatting error: %v", err)
	}

	fmt.Println("new Json:")
	fmt.Println(string(prettyJSON))

}
