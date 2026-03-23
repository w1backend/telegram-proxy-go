package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type ExternalProxy struct {
	Addr    string
	IsAlive bool
}

var proxyPool []*ExternalProxy

// 1. Получение внешнего IP
func getPublicIP() string {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "не определен (оффлайн)"
	}
	defer resp.Body.Close()
	ip, _ := io.ReadAll(resp.Body)
	return string(ip)
}

// 2. Загрузка из файла (резервный вариант)
func loadProxies(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return // Если файла нет, просто выходим
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addr := strings.TrimSpace(scanner.Text())
		if addr != "" {
			proxyPool = append(proxyPool, &ExternalProxy{Addr: addr, IsAlive: true})
		}
	}
}

// 3. Умное обновление базы
func updateProxyPool() {
	fmt.Println("[STEP] Обновление базы прокси...")
	url := "https://api.proxyscrape.com/v2/?request=displayproxies&protocol=socks5&timeout=10000&country=all&ssl=all&anonymity=all"
	
	client := http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	
	if err != nil {
		fmt.Println("[WARN] Сеть заблокирована. Использую локальный proxies.txt")
		loadProxies("proxies.txt")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	proxies := strings.Split(string(body), "\n")

	file, _ := os.Create("proxies.txt")
	defer file.Close()

	proxyPool = nil
	for _, addr := range proxies {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			file.WriteString(addr + "\n")
			proxyPool = append(proxyPool, &ExternalProxy{Addr: addr, IsAlive: true})
		}
	}
	fmt.Printf("[SUCCESS] Загружено %d прокси из сети\n", len(proxyPool))
}

// 4. Выбор лучшего прокси
func getBestProxy() string {
	for _, p := range proxyPool {
		if p.IsAlive {
			return p.Addr
		}
	}
	return ""
}

// 5. Проверка здоровья прокси
func healthCheck() {
	for {
		for _, p := range proxyPool {
			conn, err := net.DialTimeout("tcp", p.Addr, 3*time.Second)
			if err != nil {
				p.IsAlive = false
			} else {
				p.IsAlive = true
				conn.Close()
			}
		}
		time.Sleep(30 * time.Second)
	}
}

// 6. Обработка запросов (SOCKS5 Bridge)
func handleRequest(client net.Conn) {
	defer client.Close()
	buf := make([]byte, 256)
	
	n, err := client.Read(buf)
	if err != nil || n < 2 || buf[0] != 0x05 { return }
	
	client.Write([]byte{0x05, 0x00})
	client.Read(buf)

	targetAddr := getBestProxy()
	if targetAddr == "" { return }

	target, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil { return }
	defer target.Close()

	client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	
	done := make(chan bool, 2)
	go func() { io.Copy(target, client); done <- true }()
	go func() { io.Copy(client, target); done <- true }()
	<-done
}

// ГЛАВНАЯ ФУНКЦИЯ
func main() {
	fmt.Println("=== SMART PROXY SERVER STARTED ===")
	
	fmt.Println("[IP] Твой внешний адрес:", getPublicIP())

	updateProxyPool()

	if len(proxyPool) == 0 {
		fmt.Println("[ERR] Нет доступных прокси! Проверь proxies.txt или интернет.")
	}

	go healthCheck()

	l, err := net.Listen("tcp", ":8888")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[READY] Слушаю порт 8888...")

	for {
		c, err := l.Accept()
		if err != nil { continue }
		go handleRequest(c)
	}
}