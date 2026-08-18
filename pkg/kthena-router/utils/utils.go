/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/volcano-sh/kthena/pkg/kthena-router/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

var (
	KVCacheUsage      = "kv_cache_usage"
	RequestWaitingNum = "request_waiting_num"
	RequestRunningNum = "request_running_num"
	TPOT              = "TPOT"
	TTFT              = "TTFT"
)

func GetNamespaceName(obj metav1.Object) types.NamespacedName {
	return types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}

func ParsePrompt(body map[string]interface{}) (*common.ChatMessage, error) {
	if prompt, ok := body["prompt"]; ok {
		switch value := prompt.(type) {
		case string:
			return &common.ChatMessage{
				Text: value,
			}, nil
		case []interface{}:
			tokenIDs, err := parseTokenIDs(value)
			if err != nil {
				return nil, err
			}
			return &common.ChatMessage{
				TokenIDs: tokenIDs,
			}, nil
		default:
			return nil, fmt.Errorf("prompt is neither a string nor a list of token ids")
		}
	}

	if messages, ok := body["messages"]; ok {
		messageList, ok := messages.([]interface{})
		if !ok {
			return nil, fmt.Errorf("messages is not a list")
		}

		msgs := make([]common.Message, 0, len(messageList)+1)
		if systemContent, ok := parseMessageContent(body["system"]); ok {
			msgs = append(msgs, common.Message{
				Role:    "system",
				Content: systemContent,
			})
		}
		for _, message := range messageList {
			msgMap, ok := message.(map[string]interface{})
			if !ok {
				continue
			}

			role, ok := msgMap["role"].(string)
			if !ok {
				continue
			}

			content, ok := parseMessageContent(msgMap["content"])
			if !ok {
				continue
			}

			msgs = append(msgs, common.Message{
				Role:    role,
				Content: content,
			})
		}

		return &common.ChatMessage{
			Messages: msgs,
		}, nil
	}

	if input, ok := body["input"]; ok {
		return parseResponsesPrompt(body["instructions"], input)
	}

	return nil, fmt.Errorf("prompt or messages not found in request body")
}

// parseTokenIDs converts a completion prompt given as a list of token ids into
// the token ids themselves. Only a flat list of token ids is supported, which
// is the shape used by rollout engines; batched prompts are rejected.
func parseTokenIDs(values []interface{}) ([]uint32, error) {
	tokenIDs := make([]uint32, 0, len(values))
	for _, value := range values {
		var (
			id  int64
			err error
		)
		switch number := value.(type) {
		case json.Number:
			id, err = number.Int64()
		case float64:
			id = int64(number)
			if float64(id) != number {
				err = fmt.Errorf("token id %v is not an integer", number)
			}
		default:
			err = fmt.Errorf("token id %v is not a number", value)
		}
		if err != nil {
			return nil, fmt.Errorf("prompt is not a list of token ids: %w", err)
		}
		if id < 0 || id > math.MaxUint32 {
			return nil, fmt.Errorf("prompt is not a list of token ids: token id %d out of range", id)
		}
		tokenIDs = append(tokenIDs, uint32(id))
	}
	return tokenIDs, nil
}

func parseResponsesPrompt(instructions, input any) (*common.ChatMessage, error) {
	var msgs []common.Message
	if instructionText, ok := instructions.(string); ok && instructionText != "" {
		msgs = append(msgs, common.Message{Role: "developer", Content: instructionText})
	}

	parsedInput := false
	switch value := input.(type) {
	case string:
		msgs = append(msgs, common.Message{Role: "user", Content: value})
		parsedInput = true
	case []interface{}:
		// Responses input may contain only non-text content. Keep an empty
		// schedulable prompt so protocol-specific validation or the upstream
		// model can decide whether that content is supported.
		parsedInput = true
		for _, item := range value {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if itemType, ok := itemMap["type"].(string); ok && itemType != "" && itemType != "message" {
				continue
			}
			role, ok := itemMap["role"].(string)
			if !ok || role == "" {
				continue
			}
			content, ok := parseMessageContent(itemMap["content"])
			if !ok {
				continue
			}
			msgs = append(msgs, common.Message{Role: role, Content: content})
		}
	default:
		return nil, fmt.Errorf("input is not a string or list")
	}
	if !parsedInput {
		return nil, fmt.Errorf("input does not contain text")
	}
	return &common.ChatMessage{Messages: msgs}, nil
}

func parseMessageContent(content any) (string, bool) {
	if contentStr, ok := content.(string); ok {
		return contentStr, true
	}

	contentList, ok := content.([]interface{})
	if !ok {
		return "", false
	}

	parts := make([]string, 0, len(contentList))
	for _, item := range contentList {
		contentMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if contentType, ok := contentMap["type"].(string); ok {
			switch contentType {
			case "text", "input_text", "output_text":
			default:
				continue
			}
		}
		text, ok := contentMap["text"].(string)
		if !ok {
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

func GetPromptString(chatMessage *common.ChatMessage) string {
	// If Text field is present, return text directly (for prompt format)
	if chatMessage.Text != "" {
		return chatMessage.Text
	}

	// Pre-tokenized prompts are encoded as four bytes per token id so that the
	// prefix cache hashes on token instead of character boundaries and the
	// token count derived from the string length stays exact.
	if len(chatMessage.TokenIDs) > 0 {
		encoded := make([]byte, 4*len(chatMessage.TokenIDs))
		for i, tokenID := range chatMessage.TokenIDs {
			binary.BigEndian.PutUint32(encoded[i*4:], tokenID)
		}
		return string(encoded)
	}

	// For chat messages, convert to ChatML format
	var result strings.Builder
	for _, msg := range chatMessage.Messages {
		fmt.Fprintf(&result, "<|im_start|>%s\n%s<|im_end|>\n", msg.Role, msg.Content)
	}
	return result.String()
}

func LoadEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		klog.Warningf("environment variable %s is not set, using default value: %s", key, defaultValue)
		return defaultValue
	}
	return value
}
