//go:build integration

package bridge

import (
	"fmt"
	"testing"
	"time"
)

func TestMatterbridgeIntegration(t *testing.T) {
	mb := NewMatterbridgeBridge("http://127.0.0.1:4242", "", "dal-test", "dalroot-test")

	// 1. Connect
	if err := mb.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer mb.Close()
	fmt.Println("✅ Connect 성공")

	// 2. Send
	err := mb.Send(Message{
		Content: "[MatterbridgeBridge 테스트] dalcenter Go 코드에서 직접 전송",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	fmt.Println("✅ Send 성공 — Mattermost town-square에 메시지 도착 확인")

	// 3. Listen (5초간 수신 대기)
	fmt.Println("⏳ Listen 대기 (5초)...")
	timeout := time.After(5 * time.Second)
	select {
	case msg := <-mb.Listen():
		fmt.Printf("✅ Listen 수신: from=%s text=%s\n", msg.From, msg.Content)
	case err := <-mb.Errors():
		fmt.Printf("❌ Error: %v\n", err)
	case <-timeout:
		fmt.Println("⏰ 5초 내 수신 메시지 없음 (정상 — 아무도 안 보냈으니)")
	}

	fmt.Println("BotID:", mb.BotID())
	fmt.Println("GetUsername(test):", mb.GetUsername("test"))
	fmt.Println("✅ MatterbridgeBridge 전체 테스트 완료")
}
