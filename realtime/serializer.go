package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	KindUserBroadcastPush byte = 3
	KindUserBroadcast     byte = 4

	BinaryEncoding byte = 0
	JSONEncoding   byte = 1

	BroadcastEvent = "broadcast"
)

// DefaultMetadataKeys is the allowlist of metadata keys forwarded on binary broadcast push frames.
var DefaultMetadataKeys = []string{"persist"}

// Message is a Phoenix Channels wire message.
type Message struct {
	JoinRef string         `json:"join_ref"`
	Ref     string         `json:"ref"`
	Topic   string         `json:"topic"`
	Event   string         `json:"event"`
	Payload map[string]any `json:"payload"`
}

// Serializer encodes and decodes Phoenix JSON and v2 binary frames.
type Serializer struct {
	AllowedMetadataKeys []string
}

// NewSerializer returns a Serializer with default allowed metadata keys (["persist"]).
func NewSerializer(allowedMetadataKeys ...string) *Serializer {
	keys := DefaultMetadataKeys
	if len(allowedMetadataKeys) > 0 {
		keys = allowedMetadataKeys
	}
	return &Serializer{AllowedMetadataKeys: keys}
}

// Encode serializes msg to either a Phoenix v2 binary broadcast push frame (kind 3)
// or a Phoenix JSON array `[join_ref, ref, topic, event, payload]`.
func (s *Serializer) Encode(msg Message) (bool, []byte, error) {
	if msg.Event == BroadcastEvent && msg.Payload != nil {
		if userEvent, ok := msg.Payload["event"].(string); ok && userEvent != "" {
			data, err := s.encodeUserBroadcastPush(msg, userEvent)
			return true, data, err
		}
	}
	var joinRef any = nil
	if msg.JoinRef != "" {
		joinRef = msg.JoinRef
	}
	var ref any = nil
	if msg.Ref != "" {
		ref = msg.Ref
	}
	payload := msg.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	arr := []any{joinRef, ref, msg.Topic, msg.Event, payload}
	b, err := json.Marshal(arr)
	return false, b, err
}

func (s *Serializer) encodeUserBroadcastPush(msg Message, userEvent string) ([]byte, error) {
	joinRefBytes := []byte(msg.JoinRef)
	refBytes := []byte(msg.Ref)
	topicBytes := []byte(msg.Topic)
	userEventBytes := []byte(userEvent)

	allowed := s.AllowedMetadataKeys
	if allowed == nil {
		allowed = DefaultMetadataKeys
	}
	metaMap := make(map[string]any)
	for _, k := range allowed {
		if v, exists := msg.Payload[k]; exists {
			metaMap[k] = v
		}
	}
	var metaBytes []byte
	if len(metaMap) > 0 {
		var err error
		metaBytes, err = json.Marshal(metaMap)
		if err != nil {
			return nil, err
		}
	}

	if len(joinRefBytes) > 255 {
		return nil, fmt.Errorf("realtime: joinRef length %d exceeds maximum of 255", len(joinRefBytes))
	}
	if len(refBytes) > 255 {
		return nil, fmt.Errorf("realtime: ref length %d exceeds maximum of 255", len(refBytes))
	}
	if len(topicBytes) > 255 {
		return nil, fmt.Errorf("realtime: topic length %d exceeds maximum of 255", len(topicBytes))
	}
	if len(userEventBytes) > 255 {
		return nil, fmt.Errorf("realtime: userEvent length %d exceeds maximum of 255", len(userEventBytes))
	}
	if len(metaBytes) > 255 {
		return nil, fmt.Errorf("realtime: metadata length %d exceeds maximum of 255", len(metaBytes))
	}

	var encodingType byte
	var encodedPayload []byte
	rawUserPayload := msg.Payload["payload"]
	if bin, ok := rawUserPayload.([]byte); ok {
		encodingType = BinaryEncoding
		encodedPayload = bin
	} else {
		encodingType = JSONEncoding
		if rawUserPayload == nil {
			rawUserPayload = map[string]any{}
		}
		var err error
		encodedPayload, err = json.Marshal(rawUserPayload)
		if err != nil {
			return nil, err
		}
	}

	headerLen := 7 + len(joinRefBytes) + len(refBytes) + len(topicBytes) + len(userEventBytes) + len(metaBytes)
	out := make([]byte, 0, headerLen+len(encodedPayload))
	out = append(out,
		KindUserBroadcastPush,
		byte(len(joinRefBytes)),
		byte(len(refBytes)),
		byte(len(topicBytes)),
		byte(len(userEventBytes)),
		byte(len(metaBytes)),
		encodingType,
	)
	out = append(out, joinRefBytes...)
	out = append(out, refBytes...)
	out = append(out, topicBytes...)
	out = append(out, userEventBytes...)
	out = append(out, metaBytes...)
	out = append(out, encodedPayload...)
	return out, nil
}

// Decode deserializes either a binary Phoenix v2 frame (kind 4) or a Phoenix JSON array frame.
func (s *Serializer) Decode(isBinary bool, raw []byte) (*Message, error) {
	if isBinary {
		return s.decodeBinary(raw)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	if len(arr) < 5 {
		return nil, errors.New("realtime: invalid Phoenix JSON frame")
	}
	var joinRef, ref, topic, event string
	_ = json.Unmarshal(arr[0], &joinRef)
	_ = json.Unmarshal(arr[1], &ref)
	_ = json.Unmarshal(arr[2], &topic)
	_ = json.Unmarshal(arr[3], &event)
	var payload map[string]any
	_ = json.Unmarshal(arr[4], &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return &Message{
		JoinRef: joinRef,
		Ref:     ref,
		Topic:   topic,
		Event:   event,
		Payload: payload,
	}, nil
}

func (s *Serializer) decodeBinary(buf []byte) (*Message, error) {
	if len(buf) < 5 {
		return nil, errors.New("realtime: binary frame too short")
	}
	kind := buf[0]
	if kind != KindUserBroadcast {
		return nil, fmt.Errorf("realtime: unsupported binary frame kind %d", kind)
	}
	topicSize := int(buf[1])
	userEventSize := int(buf[2])
	metadataSize := int(buf[3])
	payloadEncoding := buf[4]

	offset := 5
	if len(buf) < offset+topicSize+userEventSize+metadataSize {
		return nil, errors.New("realtime: truncated binary broadcast frame")
	}
	topic := string(buf[offset : offset+topicSize])
	offset += topicSize
	userEvent := string(buf[offset : offset+userEventSize])
	offset += userEventSize
	metaBytes := buf[offset : offset+metadataSize]
	offset += metadataSize
	payloadBytes := buf[offset:]

	var parsedPayload any
	if payloadEncoding == JSONEncoding {
		if err := json.Unmarshal(payloadBytes, &parsedPayload); err != nil {
			return nil, err
		}
	} else {
		cp := make([]byte, len(payloadBytes))
		copy(cp, payloadBytes)
		parsedPayload = cp
	}

	data := map[string]any{
		"type":    BroadcastEvent,
		"event":   userEvent,
		"payload": parsedPayload,
	}
	if metadataSize > 0 {
		var meta map[string]any
		if err := json.Unmarshal(metaBytes, &meta); err != nil {
			return nil, err
		}
		data["meta"] = meta
	}

	return &Message{
		Topic:   topic,
		Event:   BroadcastEvent,
		Payload: data,
	}, nil
}
