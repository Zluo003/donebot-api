package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

type doneBotChatCompletion struct {
	choices map[int64]bool
	invalid bool
}

func (p *doneBotChatCompletion) observe(payload string) {
	if strings.TrimSpace(payload) == "[DONE]" {
		return
	}
	if !gjson.Valid(payload) {
		p.invalid = true
		return
	}
	value := gjson.Parse(payload)
	if !value.IsObject() || value.Get("error").Exists() {
		p.invalid = true
		return
	}
	for _, choice := range value.Get("choices").Array() {
		if !choice.IsObject() || choice.Get("error").Exists() {
			p.invalid = true
			continue
		}
		if p.choices == nil {
			p.choices = make(map[int64]bool)
		}
		index := choice.Get("index").Int()
		finished := false
		switch strings.TrimSpace(choice.Get("finish_reason").String()) {
		case "stop", "length", "tool_calls", "function_call", "content_filter":
			finished = true
		}
		p.choices[index] = p.choices[index] || finished
	}
}

func (p *doneBotChatCompletion) completed() bool {
	if p.invalid || len(p.choices) == 0 {
		return false
	}
	for _, finished := range p.choices {
		if !finished {
			return false
		}
	}
	return true
}
