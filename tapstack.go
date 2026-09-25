package main

// tunstack.go —— SSTap 核心方案的 Go 重写。
//
// SSTap 快的原因：TAP-Windows 虚拟网卡（固定 GUID）驱动装一次就永久常驻，
// 开关全局代理只是增删路由表条目，从不创建/销毁网卡。
//
// KNcloud 原方案（sing-box）每次开关都「杀进程 → pnputil 删残留设备 → 等待
// 网卡消失 → 重建网卡」，慢在网卡生命周期管理。
//
// 本文件用 Go 原地重写 SSTap 的三层核心：
//  1. 常驻虚拟网卡：直接调用 wintun.dll（已内嵌）以固定 GUID 创建适配器，
//     首次创建后永久复用（断电重启仍在），绝不删除；
//  2. 用户态 TCP/IP 协议栈：gvisor netstack 直接消费网卡收发包
//     （对应 SSTap 的 ss-tap/tun2socks）；
//  3. 转发出口：TCP 走 SOCKS5 CONNECT、UDP 走 SOCKS5 UDP ASSOCIATE，
//     DNS（UDP:53）经物理网卡直连公共 DNS（223.5.5.5）防污染防回环。
//
// 出口指向本机 Xray 内核的 SOCKS 入站（v2rayN 方案），两套方案就此合并：
// Xray 常驻做代理大脑，TUN 只负责「抓流量」，开关全部是秒级路由操作。

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// knTapGUID 常驻虚拟网卡的固定 GUID（对应 SSTap 的 {09794FC1-...}）。
// wintun 以 GUID 定位适配器：存在即复用，不存在才创建。
var knTapGUID = windows.GUID{
	Data1: 0x6e4a2c31,
	Data2: 0x8d5f,
	Data3: 0x4b9e,
	Data4: [8]byte{0xa2, 0x37, 0x6c, 0x15, 0xd9, 0xe4, 0x7b, 0x08},
}

// tapMTU 与 TAP-Windows 默认一致，兼容性最好
const tapMTU = 1500

// ------------------------- wintun.dll API -------------------------

var (
	wintunModPath                  string
	wintunMod                      *windows.LazyDLL
	wintunProcOpenAdapter          *windows.LazyProc
	wintunProcCreateAdapter        *windows.LazyProc
	wintunProcCloseAdapter         *windows.LazyProc
	wintunProcStartSession         *windows.LazyProc
	wintunProcEndSession           *windows.LazyProc
	wintunProcGetReadWaitEvent     *windows.LazyProc
	wintunProcReceivePacket        *windows.LazyProc
	wintunProcReleaseReceivePacket *windows.LazyProc
	wintunProcAllocateSendPacket   *windows.LazyProc
	wintunProcSendPacket           *windows.LazyProc
	wintunProcGetAdapterLUID       *windows.LazyProc
	wintunLoaded                   sync.Once
	wintunLoadErr                  error
)

// loadWintunAPI 把内嵌 wintun.dll 释放到磁盘并解析 API。
// 优先放到主程序同目录（LoadLibrary 默认搜索路径），失败则放到用户配置目录
// 并显式指定绝对路径加载。
func loadWintunAPI() error {
	wintunLoaded.Do(func() {
		writeWintunDLL := func(dst string) error {
			// 落地文件做完整性校验（SHA-256）：大小相同但内容被篡改/损坏时也要重写
			if wintunFileMatches(dst) {
				return nil
			}
			return os.WriteFile(dst, wintunDLL, 0644)
		}
		exePath, e := os.Executable()
		if e == nil {
			dst := filepath.Join(filepath.Dir(exePath), "wintun.dll")
			if we := writeWintunDLL(dst); we == nil {
				wintunModPath = dst
			}
		}
		if wintunModPath == "" {
			cfgDir, ce := appConfigDir()
			if ce != nil {
				wintunLoadErr = ce
				return
			}
			dst := filepath.Join(cfgDir, "wintun.dll")
			if we := writeWintunDLL(dst); we != nil {
				wintunLoadErr = we
				return
			}
			wintunModPath = dst
		}

		mod := windows.NewLazyDLL(wintunModPath)
		pOpen := mod.NewProc("WintunOpenAdapter")
		pCreate := mod.NewProc("WintunCreateAdapter")
		pClose := mod.NewProc("WintunCloseAdapter")
		pStart := mod.NewProc("WintunStartSession")
		pEnd := mod.NewProc("WintunEndSession")
		pEvt := mod.NewProc("WintunGetReadWaitEvent")
		pRecv := mod.NewProc("WintunReceivePacket")
		pRel := mod.NewProc("WintunReleaseReceivePacket")
		pAlloc := mod.NewProc("WintunAllocateSendPacket")
		pSend := mod.NewProc("WintunSendPacket")
		pLUID := mod.NewProc("WintunGetAdapterLUID")
		if e := mod.Load(); e != nil {
			wintunLoadErr = e
			return
		}
		for _, p := range []*windows.LazyProc{pOpen, pCreate, pClose, pStart, pEnd, pEvt, pRecv, pRel, pAlloc, pSend, pLUID} {
			if e := p.Find(); e != nil {
				wintunLoadErr = e
				return
			}
		}
		wintunMod = mod
		wintunProcOpenAdapter = pOpen
		wintunProcCreateAdapter = pCreate
		wintunProcCloseAdapter = pClose
		wintunProcStartSession = pStart
		wintunProcEndSession = pEnd
		wintunProcGetReadWaitEvent = pEvt
		wintunProcReceivePacket = pRecv
		wintunProcReleaseReceivePacket = pRel
		wintunProcAllocateSendPacket = pAlloc
		wintunProcSendPacket = pSend
		wintunProcGetAdapterLUID = pLUID
	})
	return wintunLoadErr
}

// pinnedWintunSHA256 内嵌 wintun.dll（0.14.1 amd64）的期望哈希；更新 wintun 时需同步更新。
const pinnedWintunSHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"

// wintunFileMatches 校验落地 wintun.dll 与内嵌副本一致（SHA-256 + 大小）。
func wintunFileMatches(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != int64(len(wintunDLL)) {
		return false
	}
	return fileSHA256Matches(path, pinnedWintunSHA256)
}

func wintutCreatePersistentAdapter(name string) (adapter uintptr, err error) {
	namePtr, e := windows.UTF16PtrFromString(name)
	if e != nil {
		return 0, e
	}
	typePtr, e := windows.UTF16PtrFromString("KNcloud")
	if e != nil {
		return 0, e
	}
	// Wintun 0.14 返回适配器句柄，失败时通过 GetLastError 获取错误码。
	r1, _, _ := wintunProcCreateAdapter.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(typePtr)),
		uintptr(unsafe.Pointer(&knTapGUID)),
	)
	if r1 == 0 {
		return 0, fmt.Errorf("WintunCreateAdapter failed: lasterr=%d", windows.GetLastError())
	}
	return r1, nil
}

func wintunOpenAdapterByName(name string) (uintptr, error) {
	namePtr, e := windows.UTF16PtrFromString(name)
	if e != nil {
		return 0, e
	}
	r1, _, _ := wintunProcOpenAdapter.Call(uintptr(unsafe.Pointer(namePtr)))
	if r1 != 0 {
		return r1, nil
	}
	return 0, fmt.Errorf("WintunOpenAdapter failed: lasterr=%d", windows.GetLastError())
}

func wintunStartSession(adapter uintptr, capacity uint32) (session uintptr, err error) {
	r1, _, _ := wintunProcStartSession.Call(adapter, uintptr(capacity))
	if r1 == 0 {
		return 0, fmt.Errorf("WintunStartSession failed: winerr %d", windows.GetLastError())
	}
	return r1, nil
}

// wintunAdapterIfIndex 由适配器 LUID 取系统接口索引
func wintunAdapterIfIndex(adapter uintptr) (uint32, error) {
	var luid uint64
	wintunProcGetAdapterLUID.Call(adapter, uintptr(unsafe.Pointer(&luid)))

	// 1. 优先使用 Windows NDIS 官方 LUID -> IfIndex 转换 API（无视 AF_INET 是否已在 MIB 登记）
	var ifIdx uint32
	ret, _, _ := procConvertInterfaceLuidToIndex.Call(uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&ifIdx)))
	if ret == 0 && ifIdx != 0 {
		return ifIdx, nil
	}

	// 2. 备用方式：轮询 net.InterfaceByName / GetIpInterfaceEntry
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if iface, err := net.InterfaceByName(tunIfaceName); err == nil && iface.Index > 0 {
			return uint32(iface.Index), nil
		}
		row := &windows.MibIpInterfaceRow{Family: 2 /*AF_INET*/, InterfaceLuid: luid}
		if err := windows.GetIpInterfaceEntry(row); err == nil && row.InterfaceIndex != 0 {
			return row.InterfaceIndex, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 最终兜底尝试 net.InterfaceByName
	if iface, err := net.InterfaceByName(tunIfaceName); err == nil && iface.Index > 0 {
		return uint32(iface.Index), nil
	}
	return 0, fmt.Errorf("ConvertInterfaceLuidToIndex failed (ret=%d), adapter %s not found", ret, tunIfaceName)
}

// ------------------------- 常驻适配器管理 -------------------------

// tapPersistentAdapter 进程内复用的适配器（句柄缓存）。
//
// 注意：wintun 0.14 的语义是「WintunCloseAdapter 会删除由 WintunCreateAdapter
// 创建的适配器」，因此 SSTap 那种「装一次永久常驻」做不到 —— 适配器的真实生命
// 周期是进程生命周期：进程内开关 TUN 复用句柄（秒级），进程退出后适配器随句柄
// 关闭被系统移除，下次启动 ensure() 会重新创建（同样是秒级）。
type tapPersistentAdapter struct {
	mu      sync.Mutex
	rx      sync.Mutex // readLoop 的 Receive/Release 配对互斥（同一时刻只允许一个读者推进）
	tx      sync.Mutex // WritePackets 的 Allocate/Send 互斥（与 rx 分离，避免转发回调重入自死锁）
	handle  uintptr
	session uintptr
	readEvt windows.Handle
	ifIdx   uint32
}

var knTap = &tapPersistentAdapter{}

// ensure 拿到常驻适配器并开启读写会话；已就绪则直接复用（幂等）。
func (p *tapPersistentAdapter) ensure() (ifIdx uint32, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.session != 0 {
		return p.ifIdx, nil
	}
	if err := loadWintunAPI(); err != nil {
		return 0, fmt.Errorf("wintun.dll unavailable: %v", err)
	}
	// 先尝试打开已存在的常驻适配器（SSTap 同款复用语义），不存在才创建
	adapter, aerr := wintunOpenAdapterByName(tunIfaceName)
	if aerr != nil {
		adapter, err = wintutCreatePersistentAdapter(tunIfaceName)
		if err != nil {
			return 0, fmt.Errorf("open: %v; create: %v", aerr, err)
		}
	}
	// 读写环大小：2MB 足够代理流量吞吐，内存占用可控
	session, err := wintunStartSession(adapter, 0x200000)
	if err != nil {
		wintunProcCloseAdapter.Call(adapter)
		return 0, err
	}
	ifIdx, err = wintunAdapterIfIndex(adapter)
	if err != nil {
		wintunProcEndSession.Call(session)
		wintunProcCloseAdapter.Call(adapter)
		return 0, err
	}
	p.handle = adapter
	p.session = session
	r1, _, _ := wintunProcGetReadWaitEvent.Call(session)
	p.readEvt = windows.Handle(r1)
	p.ifIdx = ifIdx
	return ifIdx, nil
}

// closeSession 结束读写会话并关闭句柄。注意：不删除适配器 —— 它是常驻的。
func (p *tapPersistentAdapter) closeSession() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != 0 {
		wintunProcEndSession.Call(p.session)
		p.session = 0
	}
	if p.handle != 0 {
		wintunProcCloseAdapter.Call(p.handle)
		p.handle = 0
	}
	p.readEvt = 0
	p.ifIdx = 0
}

// ------------------------- gvisor 链路端点 -------------------------

// tunLinkEndpoint 把 wintun 会话适配为 gvisor 的 LinkEndpoint。
// 收包：读环事件驱动，取出 IP 包按版本分发给协议栈；
// 发包：netstack 组好的 IP 包原样写回 wintun。
type tunLinkEndpoint struct {
	mtu        uint32
	adapter    *tapPersistentAdapter
	readEvt    windows.Handle
	stopCh     chan struct{}
	dispatcher stack.NetworkDispatcher
	stopped    sync.WaitGroup
	mu         sync.Mutex
	onClose    func()
}

func (e *tunLinkEndpoint) MTU() uint32                        { return e.mtu }
func (e *tunLinkEndpoint) SetMTU(mtu uint32)                  { e.mtu = mtu }
func (e *tunLinkEndpoint) MaxHeaderLength() uint16            { return 0 }
func (e *tunLinkEndpoint) LinkAddress() tcpip.LinkAddress     { return "" }
func (e *tunLinkEndpoint) SetLinkAddress(a tcpip.LinkAddress) {}
func (e *tunLinkEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return 0 // 无硬件校验和卸载：netstack 软件计算校验和
}
func (e *tunLinkEndpoint) Attach(d stack.NetworkDispatcher) {
	e.mu.Lock()
	e.dispatcher = d
	e.mu.Unlock()
	if d != nil {
		e.stopped.Add(1)
		go e.readLoop(d)
	}
}
func (e *tunLinkEndpoint) IsAttached() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dispatcher != nil
}
func (e *tunLinkEndpoint) Wait() {}

// onClose 保存协议栈注册的回调（新 gVisor LinkEndpoint 约定：
// RemoveNIC/Destroy 时栈调用 Close()，Close 须执行 SetOnCloseAction 注册的回调）。
func (e *tunLinkEndpoint) SetOnCloseAction(f func()) {
	e.mu.Lock()
	e.onClose = f
	e.mu.Unlock()
}

func (e *tunLinkEndpoint) Close() {
	e.mu.Lock()
	f := e.onClose
	e.onClose = nil
	e.mu.Unlock()
	if f != nil {
		f()
	}
}

// ARPHardwareType / AddHeader / ParseHeader：TUN 无链路层头，均为空实现
func (e *tunLinkEndpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareNone
}

func (e *tunLinkEndpoint) AddHeader(*stack.PacketBuffer) {}

func (e *tunLinkEndpoint) ParseHeader(*stack.PacketBuffer) bool { return true }

// readLoop 从 wintun 读环持续取包注入 netstack。
// WaitForSingleObject 等待读环事件（有包立即唤醒，无包 200ms 超时醒来检查 stop）。
func (e *tunLinkEndpoint) readLoop(d stack.NetworkDispatcher) {
	defer e.stopped.Done()
	// cgo 回调中的访问违例不允许带崩整个进程（尤其 gvisor 协议栈协程并发期）
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("[tapstack] readLoop panic recovered:", r)
		}
	}()
	for {
		select {
		case <-e.stopCh:
			return
		default:
		}
		if e.readEvt != 0 {
			windows.WaitForSingleObject(e.readEvt, 200)
		} else {
			time.Sleep(50 * time.Millisecond)
		}
		e.mu.Lock()
		session := e.adapter.session
		disp := e.dispatcher
		e.mu.Unlock()
		if session == 0 || disp == nil {
			return
		}
		for {
			select {
			case <-e.stopCh:
				return
			default:
			}
			e.adapter.rx.Lock()
			var size uint32
			packet, _, _ := wintunProcReceivePacket.Call(session, uintptr(unsafe.Pointer(&size)))
			if packet == 0 {
				e.adapter.rx.Unlock()
				break // ERROR_NO_MORE_ITEMS：本轮读完
			}
			// 先把包内容拷贝出来并立刻 Release —— DeliverNetworkPacket 的处理是同步的，
			// TCP 转发器可能在同 goroutine 里同步回包（如 RST），若此时还持有 rx 锁，
			// WritePackets 再取锁就会自死锁（RWMutex 不可重入）。
			data := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(packet)), size)...)
			wintunProcReleaseReceivePacket.Call(session, packet)
			e.adapter.rx.Unlock()

			proto := tcpip.NetworkProtocolNumber(0)
			if len(data) >= 1 {
				switch data[0] >> 4 {
				case 4:
					proto = ipv4.ProtocolNumber
				case 6:
					proto = ipv6.ProtocolNumber
				}
			}
			if proto == 0 {
				continue
			}
			v := buffer.NewViewWithData(data)
			var buf buffer.Buffer
			_ = buf.Append(v)
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buf})
			disp.DeliverNetworkPacket(proto, pkt)
			pkt.DecRef()
		}
	}
}

// WritePackets 把 netstack 出站 IP 包写回 wintun 发送环。
// 用独立的 tx 锁而不是 readLoop 的 rx 锁：TCP 转发回调可能在 readLoop 的
// DeliverNetworkPacket 调用栈内同步写包（RST/ACK），若共享锁必然自死锁。
// wintun 0.14 的 Receive/Release 与 Allocate/Send 两侧 API 各自线程安全，
// 收发分锁是安全的。
func (e *tunLinkEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	e.mu.Lock()
	session := e.adapter.session
	e.mu.Unlock()
	if session == 0 {
		return 0, &tcpip.ErrAborted{}
	}
	e.adapter.tx.Lock()
	defer e.adapter.tx.Unlock()
	n := 0
	for _, pkt := range pkts.AsSlice() {
		data := pkt.ToView().AsSlice()
		if len(data) == 0 {
			continue
		}
		packet, _, _ := wintunProcAllocateSendPacket.Call(session, uintptr(len(data)))
		if packet == 0 {
			continue
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(packet)), len(data)), data)
		wintunProcSendPacket.Call(session, packet)
		n++
	}
	return n, nil
}

// ------------------------- SOCKS5 客户端 -------------------------

// socksDialTCP 经 Xray 的 SOCKS5 入站建立到 dst 的 TCP 隧道（CONNECT）
func socksDialTCP(socksAddr string, dst *net.TCPAddr, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", socksAddr, timeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	// greeting: 无认证
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	rsp := make([]byte, 2)
	if _, err := io.ReadFull(conn, rsp); err != nil || rsp[0] != 0x05 || rsp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting failed")
	}
	// CONNECT：ATYP + 地址 + 端口
	var req []byte
	if ip4 := dst.IP.To4(); ip4 != nil {
		req = append(req, 0x05, 0x01, 0x00, 0x01)
		req = append(req, ip4...)
	} else {
		req = append(req, 0x05, 0x01, 0x00, 0x04)
		req = append(req, dst.IP.To16()...)
	}
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, uint16(dst.Port))
	req = append(req, port...)
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 CONNECT rejected (code=%d)", replyCode(head))
	}
	// 跳过 BIND 地址 + 端口
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x03:
		skip = 1
	case 0x04:
		skip = 16
	}
	skipBuf := make([]byte, skip+2)
	if _, err := io.ReadFull(conn, skipBuf); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func replyCode(head []byte) int {
	if len(head) > 1 {
		return int(head[1])
	}
	return -1
}

// socksUDPChannel 经 Xray SOCKS5 UDP ASSOCIATE 建立的双向 UDP 通道
type socksUDPChannel struct {
	control net.Conn     // 关联控制连接
	udp     *net.UDPConn // 关联返回的转发 socket
}

func socksDialUDP(socksAddr string, timeout time.Duration) (*socksUDPChannel, error) {
	conn, err := net.DialTimeout("tcp", socksAddr, timeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	rsp := make([]byte, 2)
	if _, err := io.ReadFull(conn, rsp); err != nil || rsp[0] != 0x05 || rsp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 greeting failed")
	}
	// UDP ASSOCIATE：绑定地址填 0（由服务端返回转发地址）
	req := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 UDP ASSOCIATE rejected (code=%d)", replyCode(head))
	}
	var bndIP net.IP
	var bndPort int
	switch head[3] {
	case 0x01:
		b := make([]byte, 6)
		if _, err := io.ReadFull(conn, b); err != nil {
			conn.Close()
			return nil, err
		}
		bndIP, bndPort = net.IP(b[:4]), int(binary.BigEndian.Uint16(b[4:]))
	case 0x04:
		b := make([]byte, 18)
		if _, err := io.ReadFull(conn, b); err != nil {
			conn.Close()
			return nil, err
		}
		bndIP, bndPort = net.IP(b[:16]), int(binary.BigEndian.Uint16(b[16:]))
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5 UDP ASSOCIATE bad ATYP %d", head[3])
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}

	if bndIP.IsUnspecified() {
		// 服务端返回 0.0.0.0：以控制连接的对端地址为准（标准行为）
		if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			bndIP = ra.IP
		}
	}
	relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: bndIP, Port: bndPort})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &socksUDPChannel{control: conn, udp: relay}, nil
}

// socksUDPHeader 打包 SOCKS5 UDP 转发头（RSV+FRAG + ATYP + 目标地址 + 端口）
func socksUDPHeader(dst *net.UDPAddr) []byte {
	var out []byte
	out = append(out, 0, 0, 0)
	if ip4 := dst.IP.To4(); ip4 != nil {
		out = append(out, 0x01)
		out = append(out, ip4...)
	} else {
		out = append(out, 0x04)
		out = append(out, dst.IP.To16()...)
	}
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, uint16(dst.Port))
	out = append(out, port...)
	return out
}

// addrToNetIP 把 gvisor 的 tcpip.Address 转成 net.IP（按地址长度选 v4/v6）
func addrToNetIP(a tcpip.Address) net.IP {
	if a.Len() == 4 {
		b := a.As4()
		return net.IP(b[:])
	}
	b := a.As16()
	return net.IP(b[:])
}

// ------------------------- 转发运行时 -------------------------

// tapForwarder TUN 开启期间的转发运行时：gvisor 协议栈 + 各转发协程
type tapForwarder struct {
	stack     *stack.Stack
	linkEP    *tunLinkEndpoint
	socksAddr string
	dnsAddr   string
	physIdx   uint32
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// startTapForwarding 启动协议栈与转发协程（幂等：运行中直接返回）
func (a *App) startTapForwarding() error {
	if a.tap != nil {
		return nil // 已在运行
	}
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	stopCh := make(chan struct{})
	link := &tunLinkEndpoint{
		mtu:     tapMTU,
		adapter: knTap,
		readEvt: knTap.readEvt,
		stopCh:  stopCh,
	}
	if err := s.CreateNIC(1, link); err != nil {
		return fmt.Errorf("gvisor CreateNIC: %v", err)
	}
	// 关键：gvisor NIC 没有配置任何 IP 地址，默认只接收「目标地址属于本机」的包，
	// 而系统路由吸进 TUN 的包目标地址五花八门（公网 IP / DNS 劫持地址 198.18.0.2）。
	// 不开 Promiscuous 模式所有包都会被丢弃（InvalidDestinationAddressesReceived）。
	// Spoofing 允许本栈以任意源地址发包（回包源=原目标，NAT 语义需要）。
	s.SetPromiscuousMode(1, true)
	s.SetSpoofing(1, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: 1},
		{Destination: header.IPv6EmptySubnet, NIC: 1},
	})

	// 物理网卡索引：DNS 直连 socket 绑定它，防止 global 模式下 DNS 回环
	physIdx := uint32(0)
	if a.tunHostRoutes != nil {
		for _, r := range a.tunHostRoutes {
			physIdx = r.IfIndex
			break
		}
	}

	f := &tapForwarder{
		stack:     s,
		linkEP:    link,
		socksAddr: fmt.Sprintf("127.0.0.1:%d", a.settings.SocksPort),
		dnsAddr:   "223.5.5.5:53",
		physIdx:   physIdx,
		stopCh:    stopCh,
	}

	// TCP：每条连接 SOCKS5 CONNECT 到原始目标
	tcpFwd := tcp.NewForwarder(s, 0, 4096, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		ep, terr := r.CreateEndpoint(&wq)
		if terr != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			conn := gonet.NewTCPConn(&wq, ep)
			dst := &net.TCPAddr{IP: addrToNetIP(id.LocalAddress), Port: int(id.LocalPort)}
			up, err := socksDialTCP(f.socksAddr, dst, 15*time.Second)
			if err != nil {
				conn.Close()
				return
			}
			done := make(chan struct{}, 2)
			go func() { io.Copy(up, conn); up.Close(); conn.Close(); done <- struct{}{} }()
			go func() { io.Copy(conn, up); conn.Close(); up.Close(); done <- struct{}{} }()
			<-done
			<-done
		}()
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// UDP：DNS（53）走物理直连防污染；其余走 SOCKS5 UDP ASSOCIATE
	// （新 gVisor：ForwarderHandler 返回 bool，true=已处理；false=回 ICMP 不可达）
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		var wq waiter.Queue
		ep, uerr := r.CreateEndpoint(&wq)
		if uerr != nil {
			return false // 端点创建失败：协议栈回 ICMP 端口不可达
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			pc := gonet.NewUDPConn(&wq, ep)
			dst := &net.UDPAddr{IP: addrToNetIP(id.LocalAddress), Port: int(id.LocalPort)}
			if dst.Port == 53 {
				f.relayDNS(pc, dst)
				return
			}
			f.relayUDPSocks(pc, dst)
		}()
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	a.tap = f
	return nil
}

// stopTapForwarding 停止协议栈与全部转发协程（网卡保留）
// 为避免 f.wg.Wait() 在 a.mu 持有期间无限阻塞（活跃 TCP 连接的 io.Copy 无超时），
// 先 close(f.stopCh) + stack.Destroy() 强制关闭全部 gvisor 端点，
// 然后在后台 goroutine 中完成 wg.Wait，不阻塞调用方。
func (a *App) stopTapForwarding() {
	if a.tap == nil {
		return
	}
	f := a.tap
	a.tap = nil

	// 1. 关闭停止通道，通知 readLoop 与所有转发协程退出
	if f.stopCh != nil {
		select {
		case <-f.stopCh:
		default:
			close(f.stopCh)
		}
	}
	if f.linkEP != nil && f.linkEP.stopCh != nil && f.linkEP.stopCh != f.stopCh {
		select {
		case <-f.linkEP.stopCh:
		default:
			close(f.linkEP.stopCh)
		}
	}

	// 2. 唤醒 readLoop 中的 WaitForSingleObject
	if knTap.readEvt != 0 {
		_ = windows.SetEvent(knTap.readEvt)
	}

	// 3. 等待 readLoop 退出，设置 400ms 超时保护，杜绝死锁阻塞 SimpleConnect
	if f.linkEP != nil {
		done := make(chan struct{})
		go func() {
			f.linkEP.stopped.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(400 * time.Millisecond):
			a.addLogInternal("warn", "TUN readLoop stopped wait timed out (400ms)")
		}
	}

	// 4. 销毁协议栈，令所有正在读写的 TCP/UDP 端点立即报错退出
	f.stack.Destroy()

	// 5. 转发协程在后台等待完全退出，不阻塞当前线程
	go f.wg.Wait()
}

// tunReapplyRoutesLocked TUN 运行中按当前活动节点与策略重铺路由
// （换节点 / 换策略共用：只动路由表，协议栈与网卡不动，秒级生效）
func (a *App) tunReapplyRoutesLocked() error {
	var node *NodeItem
	for i := range a.nodes {
		if a.nodes[i].Active {
			node = &a.nodes[i]
			break
		}
	}
	if node == nil {
		return fmt.Errorf("no node selected")
	}
	a.removeTapRouting()
	hostRoutes, _, rtErr := applySstapRoutingWithGateway(*node, a.tunIfaceIdx, a.routingMode, func() string {
		if a.nativeTunRunning() {
			return nativeSSTapRouterIP
		}
		return tunGateway
	}(), func() string {
		if a.nativeTunRunning() {
			return ""
		}
		return tunDnsAddr
	}())
	if rtErr != nil {
		return rtErr
	}
	a.tunHostRoutes = hostRoutes
	if !a.nativeTunRunning() {
		_ = addTunIPv6Route(a.tunIfaceIdx)
	}
	return nil
}

// tunSoftStopLocked TUN 软停止（tapstack 版）：停转发 + 撤路由，常驻网卡保留
func (a *App) tunSoftStopLocked() {
	a.stopNativeTun()
	a.stopTapForwarding()
	a.removeTapRouting()
	if a.tunRunning {
		a.tunRunning = false
		a.addLogInternal("info", "TUN stopped, all traffic back to direct (adapter kept installed)")
	}
	a.tunRunning = false
}

// relayDNS DNS 通道：把 TUN 内的 UDP:53 查询经绑定物理网卡的 socket 直连
// 公共 DNS（223.5.5.5）。绑定物理网卡保证 global 模式下不回环。
func (f *tapForwarder) relayDNS(pc net.PacketConn, dst *net.UDPAddr) {
	lc := net.ListenConfig{Control: bindToIfaceControl(f.physIdx)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up, err := lc.ListenPacket(ctx, "udp", "0.0.0.0:0")
	if err != nil {
		return
	}
	uc, ok := up.(*net.UDPConn)
	if !ok {
		up.Close()
		return
	}
	defer uc.Close()

	// 上行：TUN 内客户端 → 公共 DNS
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := uc.WriteTo(buf[:n], nil); err != nil {
				return
			}
		}
	}()

	// 下行：DNS 应答 → 原样回给 TUN 内客户端（From 伪装成原始目标地址）
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, _, err := uc.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := pc.WriteTo(buf[:n], dst); err != nil {
				return
			}
		}
	}()
}

// relayUDPSocks 非 DNS 的 UDP（游戏/语音等）：SOCKS5 UDP ASSOCIATE 经 Xray 转发
func (f *tapForwarder) relayUDPSocks(pc net.PacketConn, dst *net.UDPAddr) {
	ch, err := socksDialUDP(f.socksAddr, 15*time.Second)
	if err != nil {
		return
	}
	// 控制连接读错误（Xray 关闭关联）时让 pc 的读超时退出
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		buf := make([]byte, 1)
		for {
			if _, err := ch.control.Read(buf); err != nil {
				pc.SetReadDeadline(time.Now())
				return
			}
		}
	}()

	// 上行：TUN 内数据报 → SOCKS5 UDP 头封装 → 关联 socket
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		buf := make([]byte, 65535)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := ch.udp.Write(append(socksUDPHeader(dst), buf[:n]...)); err != nil {
				return
			}
		}
	}()

	// 下行：关联 socket → 解头 → 回给 TUN 内客户端（From 用上游源地址）
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer ch.control.Close()
		buf := make([]byte, 65535)
		for {
			n, err := ch.udp.Read(buf)
			if err != nil {
				return
			}
			if n <= 4 {
				continue
			}
			payload := buf[4:n]
			var from *net.UDPAddr
			switch {
			case payload[0] == 0x01 && len(payload) >= 8:
				from = &net.UDPAddr{IP: net.IP(payload[1:5]), Port: int(binary.BigEndian.Uint16(payload[5:7]))}
				payload = payload[8:]
			case payload[0] == 0x04 && len(payload) >= 20:
				from = &net.UDPAddr{IP: net.IP(payload[1:17]), Port: int(binary.BigEndian.Uint16(payload[17:19]))}
				payload = payload[20:]
			default:
				from = dst
				payload = payload[4:]
			}
			if _, err := pc.WriteTo(payload, from); err != nil {
				return
			}
		}
	}()
}

// bindToIfaceControl 返回把 socket 绑定到指定网卡的 ListenConfig Control
// （IP_UNICAST_IF，网络字节序的接口索引 —— sing-box bind_interface 同款手段）
func bindToIfaceControl(ifIdx uint32) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], ifIdx)
			v := *(*int32)(unsafe.Pointer(&b[0]))
			syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, 31 /*IP_UNICAST_IF*/, int(v))
		})
	}
}

// ------------------------- 适配器网络配置 -------------------------

// configureTapAdapter 给常驻网卡配 IP/DNS/metric（幂等：重复执行无副作用）
func configureTapAdapter(ifIdx uint32) error {
	hidden := &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	ps := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf(
			"New-NetIPAddress -InterfaceIndex %d -IPAddress %s -PrefixLength 30 -ErrorAction SilentlyContinue; "+
				"New-NetIPAddress -InterfaceIndex %d -IPAddress %s -PrefixLength 126 -ErrorAction SilentlyContinue; "+
				"Set-NetIPInterface -InterfaceIndex %d -InterfaceMetric %d; "+
				"Set-DnsClientServerAddress -InterfaceIndex %d -ServerAddresses '%s'",
			ifIdx, tunGateway,
			ifIdx, tunGateway6,
			ifIdx, tunMetric,
			ifIdx, tunDnsAddr))
	ps.SysProcAttr = hidden
	if out, err := ps.CombinedOutput(); err != nil {
		return fmt.Errorf("configure adapter: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeTapRouting 撤除 TUN 分流路由与 DNS 劫持（网卡保留，秒级重铺）
func (a *App) removeTapRouting() {
	if n := removeHostRoutes(&a.tunHostRoutes); n > 0 {
		a.addLogInternal("info", fmt.Sprintf("Removed %d node host routes", n))
	}
	if idx := a.tunIfaceIdx; idx != 0 {
		if n := deleteRoutesOnInterface(idx); n > 0 {
			a.addLogInternal("info", fmt.Sprintf("Removed %d split routes", n))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		psCmd := fmt.Sprintf(
			"Remove-NetRoute -InterfaceIndex %d -DestinationPrefix '2000::/3' -Confirm:$false -ErrorAction SilentlyContinue; "+
				"Set-DnsClientServerAddress -InterfaceIndex %d -ResetServerAddresses -ErrorAction SilentlyContinue",
			idx, idx)
		ps := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		ps.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		_ = ps.Run()
	}
}

// ------------------------- 对外开关（替换 sing-box 路径） -------------------------

// tunHealthCheck TUN 启动后的端到端自检：绑定 TUN 网卡发起一次 TCP 连接，
// 目标 IP 选择「当前策略下必被分流路由吸进 TUN」的地址。TCP 握手能完成即证明
// 「分流路由 → 转发引擎（gvisor/lwIP）→ SOCKS → Xray → 节点 → 目标」整条链路通畅。
// 失败由调用方整体回滚，避免「开关显示已连接、实际全部断网」的静默故障。
func tunHealthCheck(policy string, tunIdx uint32, dialTimeout time.Duration) error {
	if tunIdx == 0 {
		return fmt.Errorf("TUN health check: interface index unknown")
	}
	target := "1.1.1.1:443" // 非 CN 地址：bypass-cn / global / direct（bypass 语义）策略都进 TUN
	if cnPolicy, err := policyProxiesCN(policy); err == nil && cnPolicy {
		target = "223.5.5.5:443" // proxy-cn 类策略只有 CN 网段进 TUN，改用国内目标
	}
	dialer := &net.Dialer{Timeout: dialTimeout, Control: bindToIfaceControl(tunIdx)}
	deadline := time.Now().Add(dialTimeout * 3)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		conn, err := dialer.Dial("tcp", target)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(600 * time.Millisecond)
	}
	return fmt.Errorf("TUN health check failed: TCP %s not reachable through TUN (%v)", target, lastErr)
}

// policyProxiesCN 判断策略是否为「仅 CN 网段进 TUN」（proxy-cn 或 Skip=0 的 sstap 规则）。
func policyProxiesCN(policy string) (bool, error) {
	if policy == "proxy-cn" {
		return true, nil
	}
	if strings.HasPrefix(policy, "sstap:") {
		r, err := parseSstapRuleFile(strings.TrimPrefix(policy, "sstap:"))
		if err != nil {
			return false, err
		}
		return !r.Skip, nil
	}
	return false, nil
}

// SimpleConnect 简易/仪表盘的 TUN 开关：合并架构下 Xray 内核常驻作代理大脑，
// TUN 只是「抓流量」的开关 —— 全程只动路由表与转发协程，秒级生效。
// 与系统代理互斥：TUN 开启时关闭系统代理（浏览器流量交由虚拟网卡接管），
// 关闭时若内核在线则恢复系统代理（回到代理模式）。
func (a *App) SimpleConnect(start bool) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if start {
		if !isElevated() {
			// asInvoker 清单：应用以普通权限启动，只在真正需要 TUN 时提权
			// （UAC 同意后重启自身并自动进入 TUN 模式）。自检模式不弹提权。
			if selftestMode {
				a.addLogInternal("error", "TUN mode requires administrator privileges")
				return a.tunRunning, fmt.Errorf("TUN global proxy requires administrator privileges")
			}
			if rerr := relaunchElevatedForTun(a); rerr != nil {
				a.addLogInternal("error", fmt.Sprintf("TUN elevation: %v", rerr))
				return a.tunRunning, rerr
			}
			return a.tunRunning, errRelaunchRequested
		}
		var node *NodeItem
		for i := range a.nodes {
			if a.nodes[i].Active {
				node = &a.nodes[i]
				break
			}
		}
		if node == nil {
			return a.tunRunning, fmt.Errorf("no node selected")
		}

		// 1) Xray 内核（代理大脑）必须在线；未运行则静默拉起
		if !a.coreRunning {
			if err := a.startCoreLocked(); err != nil {
				a.addLogInternal("error", fmt.Sprintf("TUN: start core failed: %v", err))
				return a.tunRunning, fmt.Errorf("start core failed: %v", err)
			}
			a.coreRunning = true
			a.addLogInternal("info", "Core proxy auto-started for TUN mode")
		}

		// Prefer the native C/lwIP tun2socks engine used by SSTap. It consumes the
		// installed TAP-Windows adapter directly and avoids the Go/gVisor path.
		if nerr := a.startNativeTun(*node); nerr == nil {
			hostRoutes, nRouted, rtErr := applySstapRoutingWithGateway(*node, a.tunIfaceIdx, a.routingMode, nativeSSTapRouterIP, "")
			if rtErr != nil {
				a.stopNativeTun()
				a.removeTapRouting()
				return a.tunRunning, fmt.Errorf("SSTap routing setup failed: %v", rtErr)
			}
			if hcErr := tunHealthCheck(a.routingMode, a.tunIfaceIdx, 4*time.Second); hcErr != nil {
				a.addLogInternal("error", hcErr.Error())
				a.tunSoftStopLocked()
				return a.tunRunning, fmt.Errorf("native engine started but failed end-to-end check (node unreachable?): %v", hcErr)
			}
			a.tunHostRoutes = hostRoutes
			a.tunRunning = true
			a.disableSystemProxyForTun()
			a.addLogInternal("info", fmt.Sprintf("SSTap native engine ready | %d routes | policy %s | node: %s", nRouted, a.routingMode, node.Name))
			a.savePersisted()
			tray.requestRebuild()
			return a.tunRunning, nil
		} else {
			// 引擎未安装（发行版只带 exe）或 TAP 适配器缺失：落日志后走内置 gvisor 路径
			a.addLogInternal("info", fmt.Sprintf("Native tun2socks engine unavailable (%v), using built-in gvisor stack", nerr))
		}

		// 2) 常驻虚拟网卡（进程内复用句柄；进程退出后适配器随 wintun 语义被移除，下次启动重建）
		ifIdx, err := knTap.ensure()
		if err != nil {
			a.addLogInternal("error", fmt.Sprintf("TUN: adapter error: %v", err))
			return a.tunRunning, fmt.Errorf("adapter error: %v", err)
		}
		a.tunIfaceIdx = ifIdx
		a.addLogInternal("info", fmt.Sprintf("TUN: adapter %s ready (ifIdx %d)", tunIfaceName, ifIdx))
		if err := configureTapAdapter(ifIdx); err != nil {
			a.addLogInternal("error", fmt.Sprintf("TUN: configure adapter failed: %v", err))
			return a.tunRunning, err
		}

		// 3) 铺路由（DNS 劫持 + 节点 /32 防回环 + 按策略分流）并启动转发
		hostRoutes, nRouted, rtErr := applySstapRouting(*node, ifIdx, a.routingMode)
		if rtErr != nil {
			a.addLogInternal("error", fmt.Sprintf("TUN: routing setup failed: %v", rtErr))
			a.removeTapRouting()
			return a.tunRunning, fmt.Errorf("SSTap routing setup failed: %v", rtErr)
		}
		a.tunHostRoutes = hostRoutes
		addTunIPv6Route(ifIdx)
		if err := a.startTapForwarding(); err != nil {
			a.addLogInternal("error", fmt.Sprintf("TUN: forwarding stack failed: %v", err))
			a.removeTapRouting()
			return a.tunRunning, fmt.Errorf("forwarding stack failed: %v", err)
		}
		if hcErr := tunHealthCheck(a.routingMode, ifIdx, 4*time.Second); hcErr != nil {
			a.addLogInternal("error", hcErr.Error())
			a.tunSoftStopLocked()
			return a.tunRunning, fmt.Errorf("TUN started but failed end-to-end check (node unreachable?): %v", hcErr)
		}

		a.tunRunning = true
		a.disableSystemProxyForTun()
		a.addLogInternal("info", fmt.Sprintf("TUN interface %s ready | %d routes | policy %s | node: %s | egress: Xray SOCKS",
			tunIfaceName, nRouted, a.routingMode, node.Name))
	} else {
		// 关闭：停转发 + 撤路由 + 复位接口索引；适配器句柄进程内保留，下次开启秒级生效
		a.stopNativeTun()
		a.stopTapForwarding()
		a.removeTapRouting()
		wasRunning := a.tunRunning
		a.tunRunning = false
		a.tunIfaceIdx = 0
		if wasRunning {
			a.addLogInternal("info", "TUN stopped, all traffic back to direct (adapter kept for fast restart)")
			a.restoreSystemProxyAfterTun()
		}
	}
	a.savePersisted()
	tray.requestRebuild()
	return a.tunRunning, nil
}

// disableSystemProxyForTun TUN 接管后关闭系统代理，避免浏览器流量绕过虚拟网卡。
func (a *App) disableSystemProxyForTun() {
	if a.systemProxy {
		if err := setWindowsSystemProxy(false, ""); err != nil {
			a.addLogInternal("error", fmt.Sprintf("Failed to disable system proxy for TUN: %v", err))
			return
		}
		a.systemProxy = false
		a.addLogInternal("info", "System proxy disabled, traffic now taken over by TUN adapter")
	}
}

// restoreSystemProxyAfterTun TUN 关闭后恢复代理模式（内核在线时重新挂系统代理）。
func (a *App) restoreSystemProxyAfterTun() {
	if !a.systemProxy && a.coreRunning {
		server := fmt.Sprintf("127.0.0.1:%d", a.settings.HttpPort)
		if err := setWindowsSystemProxy(true, server); err != nil {
			a.addLogInternal("error", fmt.Sprintf("Failed to restore system proxy after TUN stop: %v", err))
			return
		}
		a.systemProxy = true
		a.addLogInternal("info", fmt.Sprintf("System proxy restored -> %s (proxy mode)", server))
	}
}
