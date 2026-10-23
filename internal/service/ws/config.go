package ws

import "github.com/ARM-auto-place/chats/internal/config"

// Configure выставляет пакетные настройки клиента и хаба. Вызывать до NewHub.
func Configure(cfg config.WSConfig) {
	WriteWait = cfg.WSClient.WriteWait
	PongWait = cfg.WSClient.PongWait
	PingPeriod = cfg.WSClient.PingPeriod
	MaxMessageSize = cfg.WSClient.MaxMessageSize
	SendBufferSize = cfg.WSClient.SendBufferSize
	ReadWorkerCount = cfg.WSClient.ReadWorkerCount
	ReadWorkerBufferSize = cfg.WSClient.ReadWorkerBufferSize
	ReadWorkerMaxRetries = cfg.WSClient.ReadWorkerMaxRetries
	ReadWorkerRetryDelay = cfg.WSClient.ReadWorkerRetryDelay

	RegisterBuf = cfg.WSHub.RegisterBuf
	UnregisterBuf = cfg.WSHub.UnregisterBuf
	BroadcastBuf = cfg.WSHub.BroadcastBuf
	ShardCount = cfg.WSHub.ShardCount
}
