// Package server implements a TCP server with RESP protocol support,
// backward compatible with IgnisDB v1 clients.
package server

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buraksocial/IgnisDB-Go/internal/engine"
)

// Server is a TCP server with RESP protocol support.
type Server struct {
	engine  *engine.LSMEngine
	addr    string
	lists   map[string][][]byte
	hashes  map[string]map[string][]byte
	sets    map[string]map[string]struct{}
	ttls    map[string]float64 // key -> expiry timestamp
	mu      sync.RWMutex
	running bool
}

// New creates a new RESP server.
func New(eng *engine.LSMEngine, addr string) *Server {
	return &Server{
		engine: eng,
		addr:   addr,
		lists:  make(map[string][][]byte),
		hashes: make(map[string]map[string][]byte),
		sets:   make(map[string]map[string]struct{}),
		ttls:   make(map[string]float64),
	}
}

// ListenAndServe starts the server.
func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	s.running = true
	log.Printf("IgnisDB-Go listening on %s", s.addr)

	for s.running {
		conn, err := listener.Accept()
		if err != nil {
			if s.running {
				log.Printf("Accept error: %v", err)
			}
			continue
		}
		go s.handleConn(conn)
	}
	return nil
}

// Stop stops the server.
func (s *Server) Stop() {
	s.running = false
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for s.running {
		cmd, args, err := parseRESP(reader)
		if err != nil {
			return
		}

		response := s.execute(cmd, args)
		writeRESP(writer, response)
		writer.Flush()
	}
}

func (s *Server) execute(cmd string, args [][]byte) interface{} {
	switch strings.ToUpper(cmd) {
	// === String Commands ===
	case "GET":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'get'")
		}
		key := args[0]
		// Check TTL
		s.mu.RLock()
		expiry, hasTTL := s.ttls[string(key)]
		s.mu.RUnlock()
		if hasTTL && float64(time.Now().UnixNano())/1e9 > expiry {
			s.engine.Delete(key)
			s.mu.Lock()
			delete(s.ttls, string(key))
			s.mu.Unlock()
			return nil
		}
		value, ok := s.engine.Get(key)
		if !ok {
			return nil
		}
		return value

	case "SET":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'set'")
		}
		key := args[0]
		value := args[1]
		s.engine.Put(key, value)

		// Handle EX/PX options
		i := 2
		for i < len(args) {
			opt := strings.ToUpper(string(args[i]))
			if opt == "EX" && i+1 < len(args) {
				ttl, _ := strconv.Atoi(string(args[i+1]))
				s.mu.Lock()
				s.ttls[string(key)] = float64(time.Now().UnixNano())/1e9 + float64(ttl)
				s.mu.Unlock()
				i += 2
			} else if opt == "PX" && i+1 < len(args) {
				ttlMs, _ := strconv.Atoi(string(args[i+1]))
				s.mu.Lock()
				s.ttls[string(key)] = float64(time.Now().UnixNano())/1e9 + float64(ttlMs)/1000
				s.mu.Unlock()
				i += 2
			} else {
				i++
			}
		}
		return "OK"

	case "DEL", "DELETE":
		if len(args) < 1 {
			return fmt.Errorf("wrong number of arguments for 'del'")
		}
		count := 0
		for _, arg := range args {
			if s.engine.Delete(arg) {
				count++
			}
			s.mu.Lock()
			delete(s.ttls, string(arg))
			s.mu.Unlock()
		}
		return int64(count)

	case "EXISTS":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'exists'")
		}
		_, ok := s.engine.Get(args[0])
		if ok {
			return int64(1)
		}
		return int64(0)

	case "EXPIRE":
		if len(args) != 2 {
			return fmt.Errorf("wrong number of arguments for 'expire'")
		}
		seconds, _ := strconv.Atoi(string(args[1]))
		_, ok := s.engine.Get(args[0])
		if ok {
			s.mu.Lock()
			s.ttls[string(args[0])] = float64(time.Now().UnixNano())/1e9 + float64(seconds)
			s.mu.Unlock()
			return int64(1)
		}
		return int64(0)

	case "TTL":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'ttl'")
		}
		s.mu.RLock()
		expiry, hasTTL := s.ttls[string(args[0])]
		s.mu.RUnlock()
		if !hasTTL {
			_, ok := s.engine.Get(args[0])
			if ok {
				return int64(-1)
			}
			return int64(-2)
		}
		remaining := expiry - float64(time.Now().UnixNano())/1e9
		if remaining <= 0 {
			s.engine.Delete(args[0])
			s.mu.Lock()
			delete(s.ttls, string(args[0]))
			s.mu.Unlock()
			return int64(-2)
		}
		return int64(remaining)

	case "PERSIST":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'persist'")
		}
		s.mu.Lock()
		if _, ok := s.ttls[string(args[0])]; ok {
			delete(s.ttls, string(args[0]))
			s.mu.Unlock()
			return int64(1)
		}
		s.mu.Unlock()
		return int64(0)

	// === List Commands ===
	case "LPUSH":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'lpush'")
		}
		key := string(args[0])
		s.mu.Lock()
		if _, ok := s.lists[key]; !ok {
			s.lists[key] = nil
		}
		for _, val := range args[1:] {
			v := make([]byte, len(val))
			copy(v, val)
			s.lists[key] = append([][]byte{v}, s.lists[key]...)
		}
		length := len(s.lists[key])
		s.mu.Unlock()
		return int64(length)

	case "RPUSH":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'rpush'")
		}
		key := string(args[0])
		s.mu.Lock()
		if _, ok := s.lists[key]; !ok {
			s.lists[key] = nil
		}
		for _, val := range args[1:] {
			v := make([]byte, len(val))
			copy(v, val)
			s.lists[key] = append(s.lists[key], v)
		}
		length := len(s.lists[key])
		s.mu.Unlock()
		return int64(length)

	case "LRANGE":
		if len(args) != 3 {
			return fmt.Errorf("wrong number of arguments for 'lrange'")
		}
		key := string(args[0])
		start, _ := strconv.Atoi(string(args[1]))
		stop, _ := strconv.Atoi(string(args[2]))
		s.mu.RLock()
		lst := s.lists[key]
		s.mu.RUnlock()
		if lst == nil {
			return []interface{}{}
		}
		n := len(lst)
		if stop == -1 {
			stop = n - 1
		}
		if start < 0 {
			start = max(0, n+start)
		}
		if stop < 0 {
			stop = n + stop
		}
		start = max(0, min(start, n-1))
		stop = max(0, min(stop, n-1))
		result := make([]interface{}, 0)
		for i := start; i <= stop && i < n; i++ {
			result = append(result, lst[i])
		}
		return result

	case "LLEN":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'llen'")
		}
		s.mu.RLock()
		length := len(s.lists[string(args[0])])
		s.mu.RUnlock()
		return int64(length)

	// === Hash Commands ===
	case "HSET":
		if len(args) != 3 {
			return fmt.Errorf("wrong number of arguments for 'hset'")
		}
		key := string(args[0])
		field := string(args[1])
		s.mu.Lock()
		if _, ok := s.hashes[key]; !ok {
			s.hashes[key] = make(map[string][]byte)
		}
		isNew := int64(1)
		if _, ok := s.hashes[key][field]; ok {
			isNew = 0
		}
		v := make([]byte, len(args[2]))
		copy(v, args[2])
		s.hashes[key][field] = v
		s.mu.Unlock()
		return isNew

	case "HGET":
		if len(args) != 2 {
			return fmt.Errorf("wrong number of arguments for 'hget'")
		}
		s.mu.RLock()
		h := s.hashes[string(args[0])]
		s.mu.RUnlock()
		if h == nil {
			return nil
		}
		v, ok := h[string(args[1])]
		if !ok {
			return nil
		}
		return v

	case "HGETALL":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'hgetall'")
		}
		s.mu.RLock()
		h := s.hashes[string(args[0])]
		s.mu.RUnlock()
		result := make([]interface{}, 0)
		for f, v := range h {
			result = append(result, []byte(f), v)
		}
		return result

	case "HDEL":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'hdel'")
		}
		s.mu.Lock()
		h := s.hashes[string(args[0])]
		count := int64(0)
		if h != nil {
			for _, field := range args[1:] {
				if _, ok := h[string(field)]; ok {
					delete(h, string(field))
					count++
				}
			}
		}
		s.mu.Unlock()
		return count

	// === Set Commands ===
	case "SADD":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'sadd'")
		}
		key := string(args[0])
		s.mu.Lock()
		if _, ok := s.sets[key]; !ok {
			s.sets[key] = make(map[string]struct{})
		}
		count := int64(0)
		for _, member := range args[1:] {
			m := string(member)
			if _, ok := s.sets[key][m]; !ok {
				s.sets[key][m] = struct{}{}
				count++
			}
		}
		s.mu.Unlock()
		return count

	case "SMEMBERS":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'smembers'")
		}
		s.mu.RLock()
		set := s.sets[string(args[0])]
		s.mu.RUnlock()
		result := make([]interface{}, 0, len(set))
		for m := range set {
			result = append(result, []byte(m))
		}
		return result

	case "SISMEMBER":
		if len(args) != 2 {
			return fmt.Errorf("wrong number of arguments for 'sismember'")
		}
		s.mu.RLock()
		set := s.sets[string(args[0])]
		s.mu.RUnlock()
		if set == nil {
			return int64(0)
		}
		if _, ok := set[string(args[1])]; ok {
			return int64(1)
		}
		return int64(0)

	case "SREM":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'srem'")
		}
		s.mu.Lock()
		set := s.sets[string(args[0])]
		count := int64(0)
		if set != nil {
			for _, member := range args[1:] {
				m := string(member)
				if _, ok := set[m]; ok {
					delete(set, m)
					count++
				}
			}
		}
		s.mu.Unlock()
		return count

	// === Sorted Set Commands ===
	case "ZADD":
		if len(args) < 3 || len(args)%2 == 0 {
			return fmt.Errorf("wrong number of arguments for 'zadd'")
		}
		key := args[0]
		count := int64(0)
		for i := 1; i < len(args); i += 2 {
			score, err := strconv.ParseFloat(string(args[i]), 64)
			if err != nil {
				return fmt.Errorf("invalid score: %v", err)
			}
			count += int64(s.engine.ZAdd(key, score, args[i+1]))
		}
		return count

	case "ZRANGE":
		if len(args) < 3 {
			return fmt.Errorf("wrong number of arguments for 'zrange'")
		}
		start, _ := strconv.Atoi(string(args[1]))
		stop, _ := strconv.Atoi(string(args[2]))
		members := s.engine.ZRange(args[0], start, stop)
		result := make([]interface{}, len(members))
		for i, m := range members {
			result[i] = m
		}
		return result

	case "ZREVRANGE":
		if len(args) < 3 {
			return fmt.Errorf("wrong number of arguments for 'zrevrange'")
		}
		start, _ := strconv.Atoi(string(args[1]))
		stop, _ := strconv.Atoi(string(args[2]))
		members := s.engine.ZRevRange(args[0], start, stop)
		result := make([]interface{}, len(members))
		for i, m := range members {
			result[i] = m
		}
		return result

	case "ZRANGEBYSCORE":
		if len(args) < 3 {
			return fmt.Errorf("wrong number of arguments for 'zrangebyscore'")
		}
		minScore := parseScoreArg(string(args[1]))
		maxScore := parseScoreArg(string(args[2]))
		offset, limit := 0, -1
		for i := 3; i < len(args); i++ {
			if strings.ToUpper(string(args[i])) == "LIMIT" && i+2 < len(args) {
				offset, _ = strconv.Atoi(string(args[i+1]))
				limit, _ = strconv.Atoi(string(args[i+2]))
				break
			}
		}
		members := s.engine.ZRangeByScore(args[0], minScore, maxScore, offset, limit)
		result := make([]interface{}, len(members))
		for i, m := range members {
			result[i] = m
		}
		return result

	case "ZREVRANGEBYSCORE":
		if len(args) < 3 {
			return fmt.Errorf("wrong number of arguments for 'zrevrangebyscore'")
		}
		maxScore := parseScoreArg(string(args[1]))
		minScore := parseScoreArg(string(args[2]))
		offset, limit := 0, -1
		for i := 3; i < len(args); i++ {
			if strings.ToUpper(string(args[i])) == "LIMIT" && i+2 < len(args) {
				offset, _ = strconv.Atoi(string(args[i+1]))
				limit, _ = strconv.Atoi(string(args[i+2]))
				break
			}
		}
		members := s.engine.ZRevRangeByScore(args[0], maxScore, minScore, offset, limit)
		result := make([]interface{}, len(members))
		for i, m := range members {
			result[i] = m
		}
		return result

	case "ZSCORE":
		if len(args) != 2 {
			return fmt.Errorf("wrong number of arguments for 'zscore'")
		}
		score, ok := s.engine.ZScore(args[0], args[1])
		if !ok {
			return nil
		}
		return []byte(strconv.FormatFloat(score, 'f', -1, 64))

	case "ZREM":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'zrem'")
		}
		count := int64(0)
		for _, member := range args[1:] {
			count += int64(s.engine.ZRem(args[0], member))
		}
		return count

	case "ZCARD":
		if len(args) != 1 {
			return fmt.Errorf("wrong number of arguments for 'zcard'")
		}
		return int64(s.engine.ZCard(args[0]))

	// === Full-Text Search Commands ===
	case "FT.ADD":
		if len(args) < 2 {
			return fmt.Errorf("wrong number of arguments for 'ft.add'")
		}
		docID := string(args[0])
		parts := make([]string, len(args)-1)
		for i, a := range args[1:] {
			parts[i] = string(a)
		}
		text := strings.Join(parts, " ")
		s.engine.FTAdd(docID, text)
		return "OK"

	case "FT.SEARCH":
		if len(args) < 1 {
			return fmt.Errorf("wrong number of arguments for 'ft.search'")
		}
		query := string(args[0])
		limit := 20
		for i := 1; i < len(args); i++ {
			if strings.ToUpper(string(args[i])) == "LIMIT" && i+1 < len(args) {
				limit, _ = strconv.Atoi(string(args[i+1]))
				break
			}
		}
		results := s.engine.FTSearch(query, limit)
		flat := make([]interface{}, 0, len(results)*2)
		for _, r := range results {
			flat = append(flat, []byte(r.DocID), []byte(strconv.FormatFloat(r.Score, 'f', -1, 64)))
		}
		return flat

	// === Cache Stats ===
	case "CACHE.STATS":
		stats := s.engine.GetStats()
		lines := make([]string, 0)
		for k, v := range stats {
			if m, ok := v.(map[string]interface{}); ok {
				for sk, sv := range m {
					lines = append(lines, fmt.Sprintf("%s.%s: %v", k, sk, sv))
				}
			} else {
				lines = append(lines, fmt.Sprintf("%s: %v", k, v))
			}
		}
		return []byte(strings.Join(lines, "\n"))

	// === Utility Commands ===
	case "PING":
		if len(args) > 0 {
			return args[0]
		}
		return "PONG"

	case "INFO":
		stats := s.engine.GetStats()
		lines := []string{
			"# Server",
			"ignisdb_version:2.0.0-go",
			fmt.Sprintf("uptime_in_seconds:%d", time.Now().Unix()),
			"",
			"# Storage",
			fmt.Sprintf("memtable_entries:%v", stats["memtable_entries"]),
			fmt.Sprintf("sstable_count:%v", stats["sstable_count"]),
			fmt.Sprintf("total_writes:%v", stats["writes"]),
			fmt.Sprintf("total_reads:%v", stats["reads"]),
			"",
			"# Cache",
		}
		if cacheMap, ok := stats["cache"].(map[string]interface{}); ok {
			lines = append(lines,
				fmt.Sprintf("cache_hits:%v", cacheMap["hits"]),
				fmt.Sprintf("cache_misses:%v", cacheMap["misses"]),
				fmt.Sprintf("cache_hit_rate:%v%%", cacheMap["hit_rate_pct"]),
				fmt.Sprintf("cache_memory:%v", cacheMap["hot_cache_bytes"]),
			)
		}
		return []byte(strings.Join(lines, "\n"))

	case "AUTH":
		return "OK"

	case "MULTI":
		return "OK"

	case "EXEC":
		return []interface{}{}

	case "DISCARD":
		return "OK"

	case "SUBSCRIBE":
		return "OK"

	case "PUBLISH":
		return int64(0)

	default:
		return fmt.Errorf("unknown command '%s'", cmd)
	}
}

func parseScoreArg(s string) float64 {
	switch s {
	case "-inf":
		return math.Inf(-1)
	case "+inf", "inf":
		return math.Inf(1)
	default:
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// === RESP Protocol Parsing ===

func parseRESP(reader *bufio.Reader) (string, [][]byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", nil, err
	}

	if len(line) == 0 {
		return "", nil, fmt.Errorf("empty line")
	}

	// RESP Array
	if line[0] == '*' {
		countStr := strings.TrimSpace(line[1:])
		count, err := strconv.Atoi(countStr)
		if err != nil {
			return "", nil, fmt.Errorf("invalid RESP count: %v", err)
		}

		args := make([][]byte, count)
		for i := 0; i < count; i++ {
			line, err = reader.ReadString('\n')
			if err != nil || len(line) == 0 || line[0] != '$' {
				return "", nil, fmt.Errorf("invalid RESP bulk string")
			}

			lengthStr := strings.TrimSpace(line[1:])
			length, err := strconv.Atoi(lengthStr)
			if err != nil {
				return "", nil, fmt.Errorf("invalid RESP length: %v", err)
			}

			data := make([]byte, length)
			_, err = reader.Read(data)
			if err != nil {
				return "", nil, err
			}
			reader.ReadString('\n') // Consume \r\n

			args[i] = data
		}

		if len(args) == 0 {
			return "", nil, fmt.Errorf("empty command")
		}
		cmd := string(args[0])
		return cmd, args[1:], nil
	}

	// Inline command fallback
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("empty command")
	}

	cmd := parts[0]
	args := make([][]byte, len(parts)-1)
	for i, p := range parts[1:] {
		args[i] = []byte(p)
	}
	return cmd, args, nil
}

func writeRESP(writer *bufio.Writer, response interface{}) {
	switch v := response.(type) {
	case string:
		if v == "OK" || v == "PONG" || v == "QUEUED" {
			writer.WriteString("+" + v + "\r\n")
		} else {
			writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(v), v))
		}
	case []byte:
		if v == nil {
			writer.WriteString("$-1\r\n")
		} else {
			writer.WriteString(fmt.Sprintf("$%d\r\n", len(v)))
			writer.Write(v)
			writer.WriteString("\r\n")
		}
	case int64:
		writer.WriteString(fmt.Sprintf(":%d\r\n", v))
	case int:
		writer.WriteString(fmt.Sprintf(":%d\r\n", v))
	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(s), s))
	case nil:
		writer.WriteString("$-1\r\n")
	case error:
		writer.WriteString(fmt.Sprintf("-ERR %s\r\n", v.Error()))
	case []interface{}:
		writer.WriteString(fmt.Sprintf("*%d\r\n", len(v)))
		for _, item := range v {
			writeRESP(writer, item)
		}
	default:
		s := fmt.Sprintf("%v", v)
		writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(s), s))
	}
}
