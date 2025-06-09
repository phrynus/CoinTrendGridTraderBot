package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/phrynus/ta"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
	"github.com/robfig/cron"
)

var (
	userDataListenKey string                // WebSocket 监听密钥
	futuresClient     *futures.Client       // 期货客户端
	httpTransport     http.Client           // HTTP 客户端
	cronScheduler     *cron.Cron            // 定时任务调度器
	exchangeInfo      *futures.ExchangeInfo // 交易所信息
)

// 单个交易币
type Coin struct {
	Symbol       string                   `json:"symbol"`       // 交易对
	Filters      []map[string]interface{} `json:"filters"`      // 过滤信息
	Kline5M      ta.KlineDatas            `json:"kline5M"`      // 5MK线
	Kline5MstopC chan struct{}            `json:"kline5MstopC"` // 5MK线 停止通道
	Kline1H      ta.KlineDatas            `json:"kline1H"`      // 1HK线
	Kline1HstopC chan struct{}            `json:"kline1HstopC"` // 1HK线 停止通道
}

var (
	tradingCoin Coin
)

// 初始化函数
func init() {
	// 初始化定时任务调度器
	cronScheduler = cron.New()
	futuresClient = binance.NewFuturesClient(config.APIKey, config.SecretKey)
	// 配置 HTTP 传输层
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	// 设置代理
	if config.Proxy != "" {
		if proxyURL, err := url.Parse(config.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			futures.SetWsProxyUrl(config.Proxy) // 设置 WebSocket 代理
		}
	}

	// 配置 HTTP 客户端
	httpTransport = http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
	futuresClient.HTTPClient = &httpTransport

	// 设置服务器时间同步函数
	syncServerTime := func() {
		if timeOffset, err := futuresClient.NewSetServerTimeService().Do(context.Background()); err != nil {
			log.Fatalf("同步服务器时间失败: %v\n", err)
		} else {
			log.Printf("服务器时间偏移量: %dms\n", timeOffset)
		}
	} // 执行首次时间同步
	syncServerTime()
	// 添加定时同步时间任务（每5分钟）
	cronScheduler.AddFunc("0 */5 * * * *", syncServerTime)

	// 初始化 WebSocket 监听
	userDataListenKey, err := futuresClient.NewStartUserStreamService().Do(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	// 添加定时续期监听密钥任务（每30分钟）
	cronScheduler.AddFunc("0 */30 * * * *", func() {
		futuresClient.NewKeepaliveUserStreamService().ListenKey(userDataListenKey).Do(context.Background())
	})

	// 获取交易所信息
	exchangeInfo, err = futuresClient.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	// 筛选符合条件的交易对
	for _, symbol := range exchangeInfo.Symbols {
		if symbol.QuoteAsset == "USDT" &&
			symbol.ContractType == "PERPETUAL" &&
			symbol.Status == "TRADING" &&
			symbol.BaseAsset == config.Symbol {
			tradingCoin.Symbol = symbol.Symbol
			tradingCoin.Filters = symbol.Filters
		}
	}
}

func main() {
	// 启动定时任务调度器
	cronScheduler.Start()
	defer cronScheduler.Stop()

	// 获取 1 小时 K 线数据并初始化 WebSocket 监听
	initKline(config.Symbol, "1h", &tradingCoin.Kline1H, &tradingCoin.Kline1HstopC)
	// 获取 5 分钟 K 线数据并初始化 WebSocket 监听
	initKline(config.Symbol, "5m", &tradingCoin.Kline5M, &tradingCoin.Kline5MstopC)

	// 超级趋势
	Hl2, _ := tradingCoin.Kline5M.SuperTrendPivotHl2(14, 3)
	log.Println(Hl2.GetDirection())
	log.Println(Hl2.GetBands())
	log.Println(Hl2.Value())

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	<-signalChan
}

// K线初始化
// 辅助函数，用于获取 K 线数据并初始化 WebSocket 监听
func initKline(symbol string, interval string, coin *ta.KlineDatas, stop *chan struct{}) {
	k, err := futuresClient.NewKlinesService().
		Limit(1000).
		Symbol(symbol + "USDT").
		Interval(interval).
		Do(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	*coin, err = ta.NewKlineDatas(k, true)
	if err != nil {
		log.Fatal(err)
	}

	var doneC chan struct{}
	var stopC chan struct{}
	var wsErr error
	go func() {
		for {
			doneC, stopC, wsErr = futures.WsKlineServe(symbol+"USDT", interval, func(event *futures.WsKlineEvent) {
				if event.Kline.IsFinal {
					err := (*coin).Add(event.Kline)
					if err != nil {
						log.Fatal(err, event.Kline)
					}
					(*coin).Keep_(1200)
				}
			}, func(err error) {
				// 关闭当前连接
				if stopC != nil {
					close(stopC)
				}
			})
			if wsErr == nil {
				(*stop) = stopC
				log.Println(symbol, interval, "Ws连接成功")
				<-doneC
			}
			// 等待 2 秒后重试
			time.Sleep(2 * time.Second)
			log.Println(symbol, interval, "Ws重连中...")
		}
	}()

}
