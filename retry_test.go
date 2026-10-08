package main

// 自动重试逻辑单测：错误分类 / 重试上限 / 手动重置
import (
	"testing"
	"time"
)

func TestRetriableVideoErr(t *testing.T) {
	retry := []string{
		"HTTP 500 {\"error\":{\"message\":\"Failed to reach upstream, please retry later\"}}",
		"接口限流（429），稍后再试",
		"等待超时", "Post \"https://...\": context deadline exceeded",
		"connection reset by peer", "network error", "TLS handshake timeout",
		"too many video status queries", "上传失败：EOF",
	}
	for _, s := range retry {
		if !retriableVideoErr(s) {
			t.Errorf("应可重试但返回 false：%q", s)
		}
	}
	noRetry := []string{
		"这一镜还没选首帧图",
		"垫图模式至少要有 1 张参考图",
		"第 2 张参考图找不到了：/out/x.png",
		"提示词是空的",
		"",
	}
	for _, s := range noRetry {
		if retriableVideoErr(s) {
			t.Errorf("不应重试但返回 true：%q", s)
		}
	}
}

func TestMaybeScheduleVideoRetry(t *testing.T) {
	videoRetryReset("shot:999")
	item := "shot:999"
	// 前 3 次安排重试，等待时间递增
	d1 := videoRetryDelay(1)
	d2 := videoRetryDelay(2)
	d3 := videoRetryDelay(3)
	if d1 != 60*time.Second || d2 != 180*time.Second || d3 != 300*time.Second {
		t.Errorf("递增间隔不对：%v %v %v", d1, d2, d3)
	}
	ok1, _ := maybeScheduleVideoRetry(item, "HTTP 500 failed")
	ok2, _ := maybeScheduleVideoRetry(item, "HTTP 500 failed")
	ok3, _ := maybeScheduleVideoRetry(item, "HTTP 500 failed")
	ok4, _ := maybeScheduleVideoRetry(item, "HTTP 500 failed")
	if !ok1 || !ok2 || !ok3 {
		t.Errorf("前 3 次都应安排重试：%v %v %v", ok1, ok2, ok3)
	}
	if ok4 {
		t.Error("超过 3 次不应再安排重试")
	}
	// 不可重试错误不安排
	videoRetryReset("shot:998")
	if ok, _ := maybeScheduleVideoRetry("shot:998", "这一镜还没选首帧图"); ok {
		t.Error("参数错误不应安排重试")
	}
	// 手动重做清零后又能重试 3 次
	videoRetryReset(item)
	if ok, _ := maybeScheduleVideoRetry(item, "HTTP 500 failed"); !ok {
		t.Error("重置后应能再安排重试")
	}
	videoRetryReset(item)
}
