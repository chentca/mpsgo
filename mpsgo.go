package main

//ver 3.1.1 2016.3.20
import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	//"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const RECV_BUF_LEN = 10240
const CONNTO_MIN = time.Second * 5
const CONNTO_MID = time.Minute * 1
const CONNTO_MAX = time.Minute * 2
const DialTO = time.Second * 5
const strlist1 = `
0:帮助，help;Telnet 0 to quit.
1:MPS运行列表,MPS list
2:停止一项MPS服务,Stop a MPS
3:端口转发,AtoB
4:socks5代理,Sock5
5:远程端口转发服务,MPS svr
6:远程端口资源（反向连接资源）,MPS source
7:远程端口用户,MPS user
8:运行状态信息,info
9:设置,set
t:Telnet服务,Telnet
`

var dbgflag bool = true
var Numcpu int = runtime.NumCPU()
var mpsini os.File
var autorun = "0"
var ok = []byte{0}
var quitcode = []byte{1}
var notquit bool = true
var mpsid, abnum int = 0, 0
var reads int64 = 0
var spd1, spd10, spd60 int64 = 0, 0, 0
var req chan int
var abnumad chan bool

//var bufreq chan *[]byte = make(chan *[]byte, bufmax)
var mpstab map[int]*mpsinfo
var mpssvrtab map[string]*map[string]*[]net.Conn

//var bufab []byte = make([]byte, RECV_BUF_LEN)

type mpsinfo struct { //mps信息记录
	info, lip, rip, mpsname string
	ftype, psw, id          int
	listener                net.Listener
	running                 bool
}

func main() {
	defer quiter()
	defer recover()
	var n int
	var spdn int64 = 0
	runtime.GOMAXPROCS(Numcpu)
	req = make(chan int, Numcpu)      //统计转发数量
	abnumad = make(chan bool, Numcpu) //统计转发线程数量
	mpstab = make(map[int]*mpsinfo)
	mpssvrtab = make(map[string]*map[string]*[]net.Conn)

	ini, err := os.OpenFile("mpsgo.ini", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		fmt.Println("err=", err.Error())
		panic("Open ini file err!")
	}
	r := bufio.NewReader(ini)
	str, err := r.ReadString('\n')
	str = strings.Trim(str, "\r\n")
	ini.Close()
	if str == "1" {
		autorun = "1"
		loadini()
	} else {
		autorun = "0"
	}
	go readsinf()
	go inputer()
	fmt.Println(strlist1)
	go func() {
		for notquit {
			after := time.After(time.Second)
			select {
			case <-after:
				spd1 = reads - spdn
				spdn = reads
				spd10 = spd1/10 + spd10/10*9
				spd60 = spd1/60 + spd60/60*59
			}
		}
	}()
	var f bool
	for notquit {
		select {
		case n = <-req:
			reads += int64(n)

		case f = <-abnumad:
			if f == true {
				abnum++
			} else {
				abnum--
			}
		}
	}

}

func mpsticker(this *mpsinfo) { //定期更新mps的资源
	if this.running != true {
		fmt.Println("MPS资源退出:", this.info)
		return
	}
	buf := make([]byte, 1)
	conn, err := net.DialTimeout("tcp", this.rip, DialTO) //发出资源
	dbg("send source:", this.info)
	if err != nil {
		after := time.After(DialTO)
		select {
		case <-after:
		}
		if this.running {
			go mpsticker(this)
		}
		return
	}
	_, err = conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
	conn.SetReadDeadline(time.Now().Add(CONNTO_MAX))
	_, err = conn.Read(buf)
	if buf[0] != 0 {
		conn.Close()
		if this.running {
			go mpsticker(this)
		}
		return
	}
	conn2, err := net.DialTimeout("tcp", this.lip, DialTO)
	if err == nil {
		go Atob(conn, conn2, int(this.mpsname[0]))
		go Atob(conn2, conn, int(this.mpsname[0]))
	} else {
		conn.Close()
		conn2.Close()
	}
	if this.running {
		go mpsticker(this)
	}
}

func (this *mpsinfo) mpssource() { //mps资源提供
	go mpsticker(this)
}

func mpsuser2(conn net.Conn, this *mpsinfo) {
	defer recover()
	buf := make([]byte, 1)
	conn2, err := net.DialTimeout("tcp", this.rip, DialTO)
	if err != nil {
		conn.Close()
		//delay 5
		return
	}

	_, err = conn2.Write(append([]byte{2}, []byte(this.mpsname)...)) //type user
	if err != nil {
		conn.Close()
		conn2.Close()
		return
	}

	conn2.SetReadDeadline(time.Now().Add(CONNTO_MAX))
	_, err = conn2.Read(buf)
	if err != nil || buf[0] != 0 {
		conn.Close()
		conn2.Close()
		return
	}

	go Atob(conn, conn2, int(this.mpsname[0]))
	go Atob(conn2, conn, int(this.mpsname[0]))

}

func (this *mpsinfo) mpsuser() { //mps资源使用者
	go func() {
		defer fmt.Println("MPS用户退出:", this.rip)
		defer recover()
		defer delete(mpstab, this.id)
		defer this.listener.Close()
		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				return
			}
			//fmt.Println("accept user:", conn.RemoteAddr())
			go mpsuser2(conn, this)
		}
	}()
}

func hand(source, user *[]net.Conn) { //撮合资源和用户连接
	defer recover()
	var conn = (*source)[0]
	var conn2 = (*user)[0]
	if len(*source) == 1 {
		*source = []net.Conn{}
	} else {
		*source = (*source)[1:]
	}
	if len(*user) == 1 {
		*user = []net.Conn{}
	} else {
		*user = (*user)[1:]
	}
	conn.Write(ok)
	conn2.Write(ok)

	go Atob(conn, conn2, 0)
	go Atob(conn2, conn, 0)

}

func mpssvr2(conn net.Conn) {
	defer recover()
	var bufab = make([]byte, RECV_BUF_LEN)
	conn.SetReadDeadline(time.Now().Add(CONNTO_MAX))
	n, err := conn.Read(bufab)
	if err != nil {
		conn.Close()
		return
	}
	mpsname := string(bufab[1:n])
	switch bufab[0] { //第一次握手
	case 1: //source

		if mpssvrtab[mpsname] == nil {
			source := new([]net.Conn)
			user := new([]net.Conn)
			mpssvrtab[mpsname] = &map[string]*([]net.Conn){"s": source, "u": user}
		}
		a := *mpssvrtab[mpsname]
		source := a["s"]
		user := a["u"]
		*source = append(*source, conn)
		if len(*source) > 1 {
			conn = (*source)[0]
			*source = (*source)[1:]
			conn.Write(quitcode)
			conn.Close()
		}
		if len(*user) == 0 {
			return
		}
		hand(source, user)
	case 2: //user

		if mpssvrtab[mpsname] == nil {
			source := new([]net.Conn)
			user := new([]net.Conn)
			mpssvrtab[mpsname] = &map[string]*([]net.Conn){"s": source, "u": user}
		}
		a := *mpssvrtab[mpsname]
		source := a["s"]
		user := a["u"]
		*user = append(*user, conn)
		if len(*user) > 60 {
			conn = (*user)[0]
			*user = (*user)[1:]
			conn.Write(quitcode)
			conn.Close()
		}
		if len(*source) == 0 {
			return
		}
		hand(source, user)

	default:
		conn.Close()
	}
}

func (this *mpsinfo) mpssvr() { //mps中转服务
	go func() {
		defer fmt.Println("MPS服务退出:", this.id)
		defer delete(mpstab, this.id)
		defer recover()
		defer this.listener.Close()

		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				return
			}
			go mpssvr2(conn)
		}
	}()
}

func list() string { //显示mps列表信息
	var str string = "\r\n"
	for key, info := range mpstab {
		str += fmt.Sprint(" ", key, " ", info.info, "\r\n")
	}
	if autorun == "1" {
		saveini()
	}
	return str
}

func strinputer(str string) string { //命令行交互
	var inputstr string
	fmt.Println(str)
	fmt.Scanln(&inputstr)
	return inputstr
}

func telsvrinputer(str1 string, conn net.Conn) string { //命令行交互
	conn.Write([]byte(str1 + "\n\r"))
	buf := make([]byte, 1)
	var str string = ""
	for {
		conn.SetDeadline(time.Now().Add(CONNTO_MAX))
		n, err := conn.Read(buf)

		if n == 0 || err != nil {
			return string(quitcode)
		}
		if buf[0] == 10 {
			continue
		}
		if buf[0] == 13 {
			if len(str) > 0 {
				return str
			} else {
				return " "
			}
		}
		if buf[0] == 8 {
			if len(str) > 0 {
				str = str[0 : len(str)-1]
			}
		} else {
			str += string(buf[0])
		}

	}
}

func inputer() { //cmd交互界面
	fmt.Println("")
	var inputstr string
	var inputint int
	for notquit {
		inputstr = "0"
		inputint = 0
		fmt.Scanln(&inputstr)
		if len(inputstr) != 1 {
			inputstr = "0"
		}
		switch inputstr {
		case "1": //list
			if len(mpstab) == 0 {
				fmt.Println("目前没有运行中的实例！")
				continue
			}
			fmt.Print(list())
		case "2": //remove
			if len(mpstab) == 0 {
				fmt.Println("目前没有运行中的实例！")
				continue
			}
			fmt.Print(list())
			fmt.Println("输入要关闭的索引号:")
			fmt.Scanln(&inputint)
			if mpstab[inputint].ftype < 3 {
				continue
			}

			switch mpstab[inputint].ftype {
			case 3, 4, 5, 7, 10:
				mpstab[inputint].listener.Close()
			case 6:

			default:

			}
			mpstab[inputint].running = false
			delete(mpstab, inputint)
			fmt.Print(list())
		case "3": //端口转发
			var lip, rip string
			lip = strinputer("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:")

			if lip == "*" || len(lip) == 0 {
				continue
			}
			rip = strinputer("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:")

			if rip == "*" || len(rip) == 0 {
				continue
			}
			pswstr := strinputer("转发密码或干扰码(密码小于:2,147,483,647 默认为0不加密)*取消:")

			if pswstr == "*" {
				continue
			}
			psw, _ := strconv.Atoi(pswstr)
			var listener, err = net.Listen("tcp", lip) //侦听端口
			if err != nil {
				fmt.Println("添加端口转发失败:" + err.Error())
				continue
			}
			fmt.Println("端口转发开始监听:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + "psw:" + strconv.Itoa(psw), lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
			mpstab[mpsid].ptop()
			fmt.Print(list())
		case "4": //socks
			lip := strinputer("请输入socks代理端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:")
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("添加socks代理失败:" + err.Error())
				continue
			}
			fmt.Println("socks代理开启:", lip)
			mpsid++
			//go socks45(listener, mpsid)
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 4, info: " socks代理: " + lip, id: mpsid, running: true}
			mpstab[mpsid].socks45()
			fmt.Print(list())
		case "5": //mps svr
			lip := strinputer("请输入MPS服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:")
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS服务失败:" + err.Error())
				continue
			}
			fmt.Println("MPS服务开启:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 5, info: " MPS服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].mpssvr()
			fmt.Print(list())
		case "6": //mps sourse
			lip := strinputer("请输入MPS资源端口(如：127.0.0.1:8080)*取消:")
			if lip == "*" || len(lip) == 0 {
				continue
			}
			rip := strinputer("请输入MPS服务端口(如：127.0.0.1:555)*取消:")
			if rip == "*" || len(rip) == 0 {
				continue
			}
			mpsname := strinputer("请输入MPS资源标识(如：socks5svr)*取消:")
			if mpsname == "*" || len(rip) == 0 {
				continue
			}
			fmt.Println(" MPS资源: ", lip, " 连接到: ", rip, " mpsname: ", mpsname)

			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " 连接到: " + rip + " mpsname: " + mpsname, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpssource()
			fmt.Print(list())
		case "7": //mps user
			lip := strinputer("请输入MPS用户端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:")
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS用户失败:" + err.Error())
				continue
			}
			rip := strinputer("请输入MPS服务端口(如：127.0.0.1:555)*取消:")
			if rip == "*" || len(rip) == 0 {
				continue
			}
			mpsname := strinputer("请输入MPS资源标识(如：socks5svr)*取消:")
			if mpsname == "*" || len(rip) == 0 {
				continue
			}
			fmt.Println("MPS用户开启:", lip, "mpsname:", mpsname)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, listener: listener, ftype: 7, info: " MPS用户: " + lip + " 连接到: " + rip + " mpsname: " + mpsname, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpsuser()
			fmt.Print(list())

		case "8": //status
			fmt.Println(state())

		case "9":
			var strtab = []string{"",
				"1:切换运行状态（自动保存恢复配置/手动保存恢复配置）",
				"2:读取配置并执行",
				"3:保存配置",
				"任意键:返回主菜单"}
			for _, str := range strtab {
				fmt.Println(str)
			}
			var str string
			fmt.Scanln(&str)
			switch str {
			case "1":
				if autorun == "1" {
					autorun = "0"
					fmt.Println("MPS运行状态切换到手动模式")
				} else {
					autorun = "1"
					fmt.Println("MPS运行状态切换到自动模式")
					saveini()
				}
			case "2":
				loadini()
			case "3":
				saveini()
			default:
				continue
			}
		case "t": //"t"
			lip := strinputer("请输入TelSvr服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:")
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS服务失败:" + err.Error())
				continue
			}
			fmt.Println("TelSvr服务开启:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].telsvr()
			fmt.Print(list())

		default: //help
			fmt.Print(strlist1)
			inputstr = "0"
		}

	}
}

func (this *mpsinfo) telsvr() { //TelSvr服务
	go func() {
		defer fmt.Println("TelSvr退出:", this.id)
		defer delete(mpstab, this.id)
		defer recover()
		defer this.listener.Close()

		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				fmt.Println("TelSvr接收错误", err)
				return
			}
			fmt.Println("TelSvr接收新连接。")
			go telsvr2(conn)
		}
	}()
}

func state() string {
	var str string
	str = `
状态信息:
`
	str += fmt.Sprint(" NumCPU: ", Numcpu, " 转发纤程: ", abnum, "\r\n")
	str += fmt.Sprint(" 转发数据: ", reads, "速度：秒-10秒-分钟 ", spd1, spd10, spd60, "\r\n")
	for k, v := range mpssvrtab {
		str += fmt.Sprint("mpsname:", k, " 资源:", strconv.Itoa(len(*(*v)["s"])), " 用户:", strconv.Itoa(len(*(*v)["u"])), "\r\n")
	}
	return str
}

func telsvr2(conn net.Conn) {
	conn.Write([]byte("\r\n"))
	var inpstr string
	for notquit {
		inpstr = telsvrinputer("input str:", conn)
		dbg(inpstr)
		if inpstr[0] == 1 {
			conn.Close()
			return
		}
		switch inpstr {
		case "0": //Tab
			conn.Close()

		case "1": //list
			if len(mpstab) == 0 {
				conn.Write([]byte("目前没有运行中的实例！\r\n"))
				continue
			}
			conn.Write([]byte(list()))
		case "2": //remove
			if len(mpstab) == 0 {
				conn.Write([]byte("目前没有运行中的实例！\r\n"))
				continue
			}
			conn.Write([]byte(list()))
			var str = telsvrinputer("输入要关闭的索引号:", conn)
			if str == " " {
				continue
			}
			ind, _ := strconv.Atoi(str)
			if mpstab[ind].ftype < 3 {
				continue
			}
			switch mpstab[ind].ftype {
			case 3, 4, 5, 7, 10:
				mpstab[ind].listener.Close()
			case 6:

			default:

			}
			delete(mpstab, ind)
			conn.Write([]byte(list()))
		case "3": //端口转发
			var lip, rip string
			lip = telsvrinputer("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

			if lip == "*" || lip == " " {
				continue
			}
			rip = telsvrinputer("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

			if rip == "*" || rip == " " {
				continue
			}
			pswstr := telsvrinputer("转发密码或干扰码(密码小于:2,147,483,647 默认为0不加密)*取消:", conn)

			if pswstr == "*" {
				continue
			}
			psw, _ := strconv.Atoi(pswstr)
			var listener, err = net.Listen("tcp", lip) //侦听端口
			if err != nil {
				conn.Write([]byte(fmt.Sprintln("添加端口转发失败:" + err.Error())))
				continue
			}
			conn.Write([]byte(fmt.Sprintln("端口转发开始监听:", lip)))
			mpsid++
			//go ptop(listener, rip, int32(psw), mpsid)
			mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + "psw:" + strconv.Itoa(psw), lip: lip, rip: rip, psw: psw, id: mpsid}
			mpstab[mpsid].ptop()
			conn.Write([]byte(list()))
		case "4": //socks5
			lip := telsvrinputer("请输入socks代理端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:", conn)
			if lip == "*" || lip == " " {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				conn.Write([]byte(fmt.Sprintln("添加socks代理失败:" + err.Error())))
				continue
			}
			conn.Write([]byte(fmt.Sprintln("socks代理开启:", lip)))
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 4, info: " socks代理: " + lip, id: mpsid}
			mpstab[mpsid].socks45()
			conn.Write([]byte(list()))
		case "5": //mps svr
			lip := telsvrinputer("请输入MPS服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:", conn)
			if lip == "*" || lip == " " {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				conn.Write([]byte(fmt.Sprintln("启动MPS服务失败:" + err.Error())))
				continue
			}
			conn.Write([]byte(fmt.Sprintln("MPS服务开启:", lip)))
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 5, info: " MPS服务: " + lip, id: mpsid}
			mpstab[mpsid].mpssvr()
			conn.Write([]byte(list()))
		case "6": //mps sourse
			lip := telsvrinputer("请输入MPS资源端口(如：127.0.0.1:8080)*取消:", conn)
			if lip == "*" || lip == " " {
				continue
			}
			rip := telsvrinputer("请输入MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if rip == "*" || rip == " " {
				continue
			}
			mpsname := telsvrinputer("请输入MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(rip) == 0 {
				continue
			}
			conn.Write([]byte(fmt.Sprintln("MPS资源:", lip, "连接到:", rip, "mpsname:", mpsname)))

			mpsid++
			//go mpssource(lip, rip, newticker, mpsid)
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " 连接到: " + rip, id: mpsid, mpsname: mpsname}
			mpstab[mpsid].mpssource()
			conn.Write([]byte(list()))
		case "7": //mps user
			lip := telsvrinputer("请输入MPS用户端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:", conn)
			if lip == "*" || lip == " " {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				conn.Write([]byte(fmt.Sprintln("启动MPS用户失败:" + err.Error())))
				continue
			}
			rip := telsvrinputer("请输入MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if rip == "*" || rip == " " {
				continue
			}
			mpsname := telsvrinputer("请输入MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(rip) == 0 {
				continue
			}
			conn.Write([]byte(fmt.Sprintln("MPS用户开启:", lip)))
			mpsid++
			//go mpsuser(listener, rip, mpsid)
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, listener: listener, ftype: 7, info: " MPS用户: " + lip + " 连接到: " + rip, id: mpsid, mpsname: mpsname}
			mpstab[mpsid].mpsuser()
			conn.Write([]byte(list()))

		case "8": //status
			conn.Write([]byte(state()))

		case "9":
			var str = `1:切换运行状态（自动保存恢复配置/手动保存恢复配置）
			2:读取配置并执行
			3:保存配置
			其他输入:返回主菜单`
			str = telsvrinputer(str, conn)
			switch str {
			case "1":
				if autorun == "1" {
					autorun = "0"
					conn.Write([]byte(fmt.Sprintln("MPS运行状态切换到手动模式")))
				} else {
					autorun = "1"
					conn.Write([]byte(fmt.Sprintln("MPS运行状态切换到自动模式")))
					saveini()
				}
			case "2":
				loadini()
			case "3":
				saveini()
			default:
				continue
			}
		case "t": //"t"
			lip := telsvrinputer("请输入TelSvr服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:", conn)
			if lip == "*" || lip == " " {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				conn.Write([]byte(fmt.Sprintln("启动Telsvr服务失败:" + err.Error())))
				continue
			}
			conn.Write([]byte(fmt.Sprintln("TelSvr服务开启:", lip)))
			mpsid++

			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid}
			conn.Write([]byte(list()))
			mpstab[mpsid].telsvr()
		default: //help
			var str string = strings.Replace(strlist1, "\n", "\n\r", -1)
			conn.Write([]byte(str))
		}

	}
}

func wstr(f *os.File, str string) { //写文件
	f.WriteString(str + "\r\n")
}

func saveini() { //保存配置
	os.Remove("mpsgo.ini")
	ini, err := os.OpenFile("mpsgo.ini", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		fmt.Println("err=", err.Error())
		panic("Open ini file err!")
	}
	defer ini.Close()
	wstr(ini, autorun)
	for _, v := range mpstab {
		wstr(ini, strconv.Itoa(v.ftype))
		wstr(ini, v.lip)
		if v.ftype == 3 || v.ftype == 6 || v.ftype == 7 {
			wstr(ini, v.rip)
		}
		if v.ftype == 3 {
			wstr(ini, strconv.Itoa(v.psw))
		}
		if v.ftype == 6 || v.ftype == 7 {
			wstr(ini, v.mpsname)
		}
	}
}

func loadini() {
	ini, err := os.OpenFile("mpsgo.ini", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		fmt.Println("err=", err.Error())
		panic("Open ini file err!")
	}
	defer ini.Close()
	var listener net.Listener
	r := bufio.NewReader(ini)
	str, err := r.ReadString('\n')
	autorun = strings.Trim(str, "\r\n")
	for {
		str, err = r.ReadString('\n')
		str = strings.Trim(str, "\r\n")
		if str == "" || err == io.EOF {
			break
		}
		switch str {
		case "3":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			rip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			pswstr := strings.Trim(str, "\r\n")
			psw, _ := strconv.Atoi(pswstr)
			listener, err = net.Listen("tcp", lip) //侦听端口
			if err != nil {
				fmt.Println("添加端口转发失败:" + err.Error())
				continue
			}
			fmt.Println("端口转发开始监听:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + " psw: " + strconv.Itoa(psw), lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
			mpstab[mpsid].ptop()

		case "4":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")
			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("添加socks代理失败:" + err.Error())
				continue
			}
			fmt.Println("socks代理开启:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 4, info: " socks代理: " + lip, id: mpsid, running: true}
			mpstab[mpsid].socks45()
		case "5":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")

			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS服务失败:" + err.Error())
				continue
			}
			fmt.Println("MPS服务开启:", lip)
			mpsid++
			//go mpssvr(listener, mpsid)
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 5, info: " MPS服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].mpssvr()
		case "6":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			rip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			mpsname := strings.Trim(str, "\r\n")
			fmt.Println("MPS资源:", lip, "连接到:", rip, "mpsname:", mpsname)

			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " 连接到: " + rip + " mpsname: " + mpsname, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpssource()

		case "7":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			rip := strings.Trim(str, "\r\n")
			str, err = r.ReadString('\n')
			mpsname := strings.Trim(str, "\r\n")
			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS用户失败:" + err.Error())
				continue
			}
			fmt.Println("MPS用户开启:", lip, "mpsname:", mpsname)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, listener: listener, ftype: 7, info: " MPS用户: " + lip + " 连接到: " + rip + " mpsname: " + mpsname, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpsuser()
		case "10":
			str, err = r.ReadString('\n')
			lip := strings.Trim(str, "\r\n")

			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动Telsvr服务失败:" + err.Error())
				continue
			}
			fmt.Println("Telsvr服务开启:", lip)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " Telsvr服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].telsvr()
		}
	}
	fmt.Print(list())
}

func readsinf() { //拟用于计算每秒网速
	var reads2 int64
	tc := time.Tick(time.Second)
	for {
		<-tc
		if reads != reads2 {
			reads2 = reads
			//fmt.Print(reads, " ")
		}
	}
}

func quiter() { //程序退出标志
	notquit = false
}

func resume() { //测试返回
	recover()
	fmt.Println("Recovered")
}

func ptop2(conn net.Conn, rip string, psw int) { //执行转发
	defer recover()
	conn2, err := net.Dial("tcp", rip)
	if err != nil {
		fmt.Println("conn2 err" + err.Error())
		conn.Close()
		return
	}
	//fmt.Println("conn2 :", rip)
	go Atob(conn, conn2, psw)
	go Atob(conn2, conn, psw)
}

func (this *mpsinfo) ptop() { //端口转发服务
	go func() {
		defer fmt.Println("端口转发退出:", this.rip)
		defer delete(mpstab, this.id)
		defer recover()
		defer this.listener.Close()

		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				return
			}
			go ptop2(conn, this.rip, this.psw)
		}
	}()
}

func s5(conn net.Conn, n int) {
	defer recover()

	bufab := make([]byte, RECV_BUF_LEN)
	n, err := conn.Write([]byte{5, 0})
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Now().Add(CONNTO_MAX))
	n, err = conn.Read(bufab)
	if n == 0 || err != nil {
		conn.Close()
		return
	}

	switch {
	case bytes.Equal(bufab[0:4], []byte{5, 1, 0, 1}): //ip请求
		var ip bytes.Buffer

		ip.WriteString(strconv.Itoa(int(bufab[4])) + "." +
			strconv.Itoa(int(bufab[5])) + "." +
			strconv.Itoa(int(bufab[6])) + "." +
			strconv.Itoa(int(bufab[7])) + ":" +
			strconv.Itoa(int(bufab[9])+int(bufab[8])*256))
		conn2, err := net.DialTimeout("tcp", ip.String(), DialTO)
		if err != nil {
			bufab[1] = 3
			conn.Write(bufab[0:n])
			conn.Close()
			return
		}
		bufab[1] = 0
		_, err = conn.Write(bufab[0:n])
		if err != nil {
			dbg("S5 应答错误6：" + err.Error())
			conn.Close()
			conn2.Close()
			return
		}
		go Atob(conn, conn2, 0)
		go Atob(conn2, conn, 0)

	case bytes.Equal(bufab[0:4], []byte{5, 1, 0, 3}): //域名请求
		ip := string(bufab[5:bufab[4]+5]) + ":" + strconv.Itoa(int(bufab[n-2])*256+int(bufab[n-1]))
		dbg("ip:", ip)
		conn2, err := net.DialTimeout("tcp", ip, DialTO)
		if err != nil {
			dbg("S5 连接错误7：" + err.Error())
			bufab[1] = 3
			conn.Write(bufab[0:n])
			conn.Close()
			return
		}
		bufab[1] = 0
		_, err = conn.Write(bufab[0:n])
		if err != nil {
			dbg("S5 应答错误8：" + err.Error())
			conn.Close()
			conn2.Close()
			return
		}
		go Atob(conn, conn2, 0)
		go Atob(conn2, conn, 0)

	default:
		conn.Close()
	}
}

func socksswich(conn net.Conn) { //判断代理类型
	defer recover()
	bufab := make([]byte, RECV_BUF_LEN)
	conn.SetReadDeadline(time.Now().Add(CONNTO_MAX))
	n, err := conn.Read(bufab)
	if bytes.Equal(bufab[0:2], []byte{5, 1}) && err == nil { //socks5
		go s5(conn, n)
		return
	}
	conn.Close()

}

func (this *mpsinfo) socks45() {
	go func() {
		defer fmt.Println("socks服务器退出")
		defer delete(mpstab, this.id)
		defer recover()
		defer this.listener.Close()
		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				dbg("接受代理错误1：" + err.Error())
				return
			}
			go socksswich(conn)
		}
	}()
}

func Atob(conn, conn2 net.Conn, psw int) { //数据转发
	bufab := make([]byte, RECV_BUF_LEN)
	defer recover()
	defer conn.Close()
	defer conn2.Close()
	defer func() {
		abnumad <- false
	}()

	abnumad <- true
	pswa := byte(psw)
	for notquit {

		conn.SetDeadline(time.Now().Add(CONNTO_MAX))
		n, err := conn.Read(bufab)

		if n <= 0 || err != nil {
			return
		}
		if pswa != 0 {
			for i := 0; i < n; i++ {
				bufab[i] ^= pswa
			}
		}
		n, err = conn2.Write(bufab[0:n])

		if n <= 0 || err != nil {
			return
		}
		req <- n

	}
}

func dbg(str ...interface{}) {
	if dbgflag == true {
		fmt.Println(str)
	}
}

func xor(s string, p int32) string { //异或加密
	var t = bytes.Buffer{}
	for _, j := range s {
		j ^= p
		t.WriteRune(j)
	}
	return t.String()
}
