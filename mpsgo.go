package main

//ver 3.2.4 2016.4.6
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

//GOGC = 50 //要在环境变量里修改 env
const RECV_BUF_LEN = 4096
const CONNTO_MIN = time.Second * 5
const CONNTO_MID = time.Minute * 1
const CONNTO_MAX = time.Minute * 2
const DialTO = time.Second * 5

var strlist0 []string = []string{
	"0:帮助，help;Telnet 0 to quit.",
	"1:MPS运行列表,MPS list",
	"2:停止一项MPS服务,Stop a MPS",
	"3:端口转发,AtoB",
	"4:socks5代理,Sock5",
	"5:远程端口转发服务,MPS svr",
	"6:远程端口资源（反向连接资源）,MPS source",
	"7:远程端口用户,MPS user",
	"8:运行状态信息,info",
	"9:设置,set",
	"r:资源中继,MPS source relay",
	"b:资源桥,MPS source bridge",
	"t:Telnet服务,Telnet"}

var strtab0 []string = []string{"1:切换运行状态（自动保存恢复配置/手动保存恢复配置）",
	"2:读取配置并执行",
	"3:保存配置",
	"任意键:返回主菜单"}

var dbgflag bool = false
var Numcpu int = runtime.NumCPU()
var mpsini os.File
var autorun = "0"
var ok = []byte{0}
var quitcode = []byte{1}
var notquit bool = true
var mpsid, abnum int = 0, 0
var reads int64 = 0
var spd1, spd10, spd60 int64 = 0, 0, 0
var req chan int = make(chan int, Numcpu)       //统计转发数量
var abnumad chan bool = make(chan bool, Numcpu) //统计转发线程数量
//var bufreq chan *[]byte = make(chan *[]byte, bufmax)
var mpstab map[int]*mpsinfo = make(map[int]*mpsinfo)
var mpssvrtab map[string]*map[string]*[]net.Conn = make(map[string]*map[string]*[]net.Conn)
var mpsdone map[string]int = make(map[string]int)
var strlist1, strtab string

//var bufab []byte = make([]byte, RECV_BUF_LEN)

type mpsinfo struct { //mps信息记录
	info, lip, rip, mpsname, psw string
	ftype, id                    int
	listener                     net.Listener
	running                      bool
}

type callback func(this *mpsinfo, conn net.Conn)

func main() {
	strlist1 = stradd(strlist0)
	strtab = stradd(strtab0)
	defer quiter()
	defer recover()
	var n int
	var spdn int64 = 0
	runtime.GOMAXPROCS(Numcpu)

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
	go inputer(nil, &mpsinfo{running: true})
	fmt.Println(strlist1)

	var f bool
	after := time.After(time.Second)
	for notquit {
		select {
		case <-after: //计算单位时间的传输速度
			spd1 = reads - spdn
			spdn = reads
			spd10 = spd1/10 + spd10/10*9
			spd60 = spd1/60 + spd60/60*59
			after = time.After(time.Second)

		case n = <-req: //统计转发数据量
			reads += int64(n)

		case f = <-abnumad: //统计转发纤程数量
			if f == true {
				abnum++
			} else {
				abnum--
			}
		}
	}

}

func mpssource2(this *mpsinfo, conn net.Conn) { //mps的资源
	buf := make([]byte, 1)
	conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
	conn.Read(buf)

	if buf[0] != 0 {
		conn.Close()
		return
	}
	conn2, err := net.DialTimeout("tcp", this.lip, DialTO)
	if err == nil {
		go Atob(conn, conn2, this.mpsname)
		go Atob(conn2, conn, this.mpsname)
	} else {
		conn.Close()
	}

}

func mpssvr21(conn net.Conn, mpsname string) { //资源握手判断
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
	if len(*user) > 0 {
		hand(source, user, mpsname)
	}
}

func mpssvr22(conn net.Conn, mpsname string) { //用户握手判断
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
	if len(*source) > 0 {
		hand(source, user, mpsname)
	}
}

func mpsrelay2(this *mpsinfo, conn net.Conn) { //当作mps的资源
	dbg("send source:", this.info)
	buf := make([]byte, 1)
	conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
	conn.Read(buf)

	if buf[0] != 0 {
		conn.Close()
		return
	}

	//当作用户判断握手
	if mpssvrtab[this.mpsname] == nil {
		source := new([]net.Conn)
		user := new([]net.Conn)
		mpssvrtab[this.mpsname] = &map[string]*([]net.Conn){"s": source, "u": user}
	}
	a := *mpssvrtab[this.mpsname]
	source := a["s"]

	if len(*source) > 0 {
		var conn2 = (*source)[0]
		if len(*source) == 1 {
			*source = []net.Conn{}
		} else {
			*source = (*source)[1:]
		}
		conn2.Write(ok)
		go Atob(conn, conn2, "")
		go Atob(conn2, conn, "")

		mpsdone[this.mpsname]++
	} else {
		conn.Write(quitcode)
		conn.Close()
	}

}

func mpsbridge2(this *mpsinfo, conn net.Conn) { //当作mps的资源
	dbg("send source:", this.info)
	buf := make([]byte, 1)
	conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
	conn.Read(buf)

	if buf[0] != 0 {
		conn.Close()
		return
	}

	//与下级用户发起对接
	conn2, err := net.DialTimeout("tcp", this.lip, DialTO)
	if err != nil {
		conn.Close()
		time.Sleep(5 * time.Second)
		return
	}
	conn2.Write(append([]byte{2}, []byte(this.mpsname)...)) //type user
	conn2.SetDeadline(time.Now().Add(CONNTO_MAX))
	conn2.Read(buf)
	if buf[0] != 0 {
		conn.Close()
		conn2.Close()
		return
	}
	go Atob(conn, conn2, "")
	go Atob(conn2, conn, "")
	mpsdone[this.mpsname]++
}

func (this *mpsinfo) mpssource() { //mps资源提供
	go mpsone(this, mpssource2)
}

func (this *mpsinfo) mpsrelay() { //mps资源中继
	go mpsone(this, mpsrelay2)
}

func (this *mpsinfo) mpsbridge() { //mps资源中继
	go mpsone(this, mpsbridge2)
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

	conn2.SetDeadline(time.Now().Add(CONNTO_MAX))
	_, err = conn2.Read(buf)
	if err != nil || buf[0] != 0 {
		conn.Close()
		conn2.Close()
		return
	}
	go Atob(conn, conn2, this.mpsname)
	go Atob(conn2, conn, this.mpsname)

}

func mpsone(this *mpsinfo, next callback) {
	var conn net.Conn
	var err error
	for this.running {
		for this.running {
			dbg("send source")
			conn, err = net.DialTimeout("tcp", this.rip, DialTO) //发出资源
			if err == nil {
				break
			}
			time.Sleep(5 * time.Second)

		}
		if this.running == false {
			return
		}
		next(this, conn)
	}

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
			go mpsuser2(conn, this)
		}
	}()
}

func hand(source, user *[]net.Conn, mpsname string) { //撮合资源和用户连接
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

	go Atob(conn, conn2, "")
	go Atob(conn2, conn, "")

	mpsdone[mpsname]++
}

func mpssvr2(conn net.Conn) {
	defer recover()
	var bufab = make([]byte, RECV_BUF_LEN)
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
	n, err := conn.Read(bufab)
	if err != nil {
		conn.Close()
		return
	}
	mpsname := string(bufab[1:n])
	switch bufab[0] { //第一次握手
	case 1: //source
		mpssvr21(conn, mpsname)

	case 2: //user
		mpssvr22(conn, mpsname)

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

func stradd(strtab []string) string {
	var str string = "\r\n"
	for _, info := range strtab {
		str += fmt.Sprint(info, "\r\n")
	}
	return str
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
	conn.Write([]byte(str1 + "\r\n"))
	buf := make([]byte, 1)
	var str string = ""
	for {
		conn.SetDeadline(time.Now().Add(CONNTO_MAX))
		n, err := conn.Read(buf)
		if n == 0 || err != nil {
			return "0"
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

func inputerin(str string, conn net.Conn) string {
	if conn == nil {
		return strinputer(str)
	} else {
		return telsvrinputer(str, conn)
	}
}

func inputerout(str string, conn net.Conn) {
	if conn == nil {
		fmt.Println(str)
	} else {
		conn.Write([]byte(str))
	}
}

func inputer(conn net.Conn, this *mpsinfo) { //cmd交互界面
	var inputstr string
	var inputint int
	for notquit && this.running {
		inputstr = inputerin("", conn)
		switch inputstr {
		case "1": //list
			if len(mpstab) == 0 {
				inputerout("目前没有运行中的实例！", conn)
				continue
			}
			inputerout(list(), conn)
		case "2": //remove
			if len(mpstab) == 0 {
				inputerout("目前没有运行中的实例！", conn)
				continue
			}
			inputerout(list(), conn)
			inputstr = inputerin("输入要关闭的索引号:", conn)
			inputint, _ = strconv.Atoi(inputstr)
			if inputint == 0 {
				continue
			}
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
			inputerout(list(), conn)
		case "3": //端口转发
			var lip, rip string
			lip = inputerin("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

			if lip == "*" || len(lip) == 0 {
				continue
			}
			rip = inputerin("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

			if rip == "*" || len(rip) == 0 {
				continue
			}
			psw := inputerin("转发密码或干扰码(密码小于:2,147,483,647 默认为0不加密)*取消:", conn)

			if psw == "*" {
				continue
			}

			var listener, err = net.Listen("tcp", lip) //侦听端口
			if err != nil {
				inputerout("添加端口转发失败:"+err.Error(), conn)
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + "psw:" + psw, lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
			mpstab[mpsid].ptop()
			inputerout(mpstab[mpsid].info, conn)
			inputerout(list(), conn)
		case "4": //socks
			lip := inputerin("请输入socks代理端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				inputerout("添加socks代理失败:"+err.Error(), conn)
				continue
			}
			mpsid++
			//go socks45(listener, mpsid)
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 4, info: " socks代理: " + lip, id: mpsid, running: true}
			mpstab[mpsid].socks45()
			inputerout(mpstab[mpsid].info, conn)
			inputerout(list(), conn)
		case "5": //mps svr
			lip := inputerin("请输入MPS服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS服务失败:" + err.Error())
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 5, info: " MPS服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].mpssvr()
			inputerout(mpstab[mpsid].info, conn)
			inputerout(list(), conn)
		case "6": //mps sourse
			lip := inputerin("请输入MPS资源端口(如：127.0.0.1:8080)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}
			rip := inputerin("请输入MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if rip == "*" || len(rip) == 0 {
				continue
			}
			mpsname := inputerin("请输入MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(rip) == 0 {
				continue
			}

			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " mpsname:[" + mpsname + "]" + " 连接到: " + rip, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpssource()
			inputerout(mpstab[mpsid].info, conn)
			inputerout(list(), conn)
		case "7": //mps user
			lip := inputerin("请输入MPS用户端口(如：127.0.0.1:8080 或 0.0.0.0:8080)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				inputerout("启动MPS用户失败:"+err.Error(), conn)
				continue
			}
			rip := inputerin("请输入MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if rip == "*" || len(rip) == 0 {
				continue
			}
			mpsname := inputerin("请输入MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(rip) == 0 {
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, listener: listener, ftype: 7, info: "MPS用户开启:" + lip + "mpsname:[" + mpsname + "]", id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpsuser()
			inputerout(mpstab[mpsid].info, conn)
			inputerout(list(), conn)

		case "8": //status
			inputerout(state(), conn)

		case "9": //set

			str := inputerin(strtab, conn)
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
		case "t": //Telnet Svr
			lip := inputerin("请输入TelSvr服务端口(如：127.0.0.1:555 或 0.0.0.0:555)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}
			var listener, err = net.Listen("tcp", lip)
			if err != nil {
				inputerout("启动Telsvr失败:"+err.Error(), conn)
				continue
			}

			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].telsvr()
			inputerout("TelSvr服务开启: "+lip, conn)
			inputerout(list(), conn)

		case "r": //relay 资源中继
			mpsname := inputerin("请输入需中继的MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(mpsname) == 0 {
				continue
			}
			rip := inputerin("请输入MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if rip == "*" || len(rip) == 0 {
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{ftype: 11, lip: "0", info: " MPS中继服务:[" + mpsname + "]->" + rip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
			mpstab[mpsid].mpsrelay()
			inputerout("MPS中继服务开启:"+mpstab[mpsid].info, conn)
			inputerout(list(), conn)

		case "b": //bridge 资源桥
			mpsname := inputerin("请输入需中继的MPS资源标识(如：socks5svr)*取消:", conn)
			if mpsname == "*" || len(mpsname) == 0 {
				continue
			}
			rip := inputerin("请输入上级MPS服务端口(如：127.0.0.1:555)*取消:", conn) //为了复用user函数，rip定义为上级地址
			if rip == "*" || len(rip) == 0 {
				continue
			}
			lip := inputerin("请输入下级MPS服务端口(如：127.0.0.1:555)*取消:", conn)
			if lip == "*" || len(lip) == 0 {
				continue
			}

			mpsid++
			mpstab[mpsid] = &mpsinfo{ftype: 12, info: " MPS服务桥: " + rip + "[" + mpsname + "]->" + lip, lip: lip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
			mpstab[mpsid].mpsbridge()
			inputerout("MPS服务桥开启:"+mpstab[mpsid].info, conn)
			inputerout(list(), conn)

		default: //help
			if conn != nil && inputstr == "0" {
				return
			}
			inputerout(strlist1, conn)
			inputstr = "0"
		}
	}
}

func (this *mpsinfo) telsvr() { //TelSvr服务
	go func() {
		defer fmt.Println("TelSvr退出:", this.id)
		defer delete(mpstab, this.id)
		defer recover()
		//defer this.listener.Close()
		for notquit && this.running {
			conn, err := this.listener.Accept() //接受连接
			if err != nil {
				fmt.Println("TelSvr接收错误", err)
				return
			}
			fmt.Println("TelSvr接收新连接。", conn.RemoteAddr())
			go inputer(conn, this)
		}
	}()
}

func state() string {
	var str string
	str = "状态信息:\r\n"
	str += fmt.Sprint(" NumCPU: ", Numcpu, " 转发纤程: ", abnum, "\r\n")
	str += fmt.Sprint(" 转发数据: ", reads, " 速度：秒-10秒-分钟 ", spd1, spd10, spd60, "\r\n")
	for k, v := range mpssvrtab {
		str += fmt.Sprint("mpsname:", k, " 资源:", strconv.Itoa(len(*(*v)["s"])), " 用户:", strconv.Itoa(len(*(*v)["u"])), " 对接:", mpsdone[k], "\r\n")
	}
	return str
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
		if v.ftype == 3 || v.ftype == 6 || v.ftype == 7 || v.ftype == 11 || v.ftype == 12 {
			wstr(ini, v.rip)
		}
		if v.ftype == 3 {
			wstr(ini, v.psw)
		}
		if v.ftype == 6 || v.ftype == 7 || v.ftype == 11 || v.ftype == 12 {
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
		str := iniloadln(r)
		if str == "" || err == io.EOF {
			break
		}
		switch str {
		case "3":
			lip := iniloadln(r)
			rip := iniloadln(r)
			psw := iniloadln(r)

			listener, err = net.Listen("tcp", lip) //侦听端口
			if err != nil {
				fmt.Println("添加端口转发失败:" + err.Error())
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + "psw:" + psw, lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
			mpstab[mpsid].ptop()
			fmt.Println(mpstab[mpsid].info)

		case "4":
			lip := iniloadln(r)
			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("添加socks代理失败:" + err.Error())
				continue
			}

			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 4, info: " socks代理: " + lip, id: mpsid, running: true}
			mpstab[mpsid].socks45()
			fmt.Println(mpstab[mpsid].info)

		case "5":
			lip := iniloadln(r)

			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS服务失败:" + err.Error())
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 5, info: " MPS服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].mpssvr()
			fmt.Println(mpstab[mpsid].info)

		case "6":
			lip := iniloadln(r)
			rip := iniloadln(r)
			mpsname := iniloadln(r)
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " mpsname:[" + mpsname + "]" + " 连接到: " + rip, id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpssource()
			fmt.Println(mpstab[mpsid].info)

		case "7":
			lip := iniloadln(r)
			rip := iniloadln(r)

			mpsname := iniloadln(r)
			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动MPS用户失败:" + err.Error())
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, listener: listener, ftype: 7, info: "MPS用户开启:" + lip + "mpsname:[" + mpsname + "]", id: mpsid, running: true, mpsname: mpsname}
			mpstab[mpsid].mpsuser()
			fmt.Println(mpstab[mpsid].info)

		case "10":
			lip := iniloadln(r)
			listener, err = net.Listen("tcp", lip)
			if err != nil {
				fmt.Println("启动Telsvr服务失败:" + err.Error())
				continue
			}
			mpsid++
			mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid, running: true}
			mpstab[mpsid].telsvr()
			fmt.Println(mpstab[mpsid].info)

		case "11":
			_ = iniloadln(r)
			rip := iniloadln(r)
			mpsname := iniloadln(r)

			mpsid++
			mpstab[mpsid] = &mpsinfo{ftype: 11, lip: "0", info: " MPS中继服务:[" + mpsname + "]->" + rip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
			mpstab[mpsid].mpsrelay()
			fmt.Println(mpstab[mpsid].info)

		case "12":
			lip := iniloadln(r)
			rip := iniloadln(r)
			mpsname := iniloadln(r)

			mpsid++
			mpstab[mpsid] = &mpsinfo{ftype: 12, info: " MPS服务桥: " + rip + "[" + mpsname + "]->" + lip, lip: lip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
			mpstab[mpsid].mpsbridge()
			fmt.Println(mpstab[mpsid].info)
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

func ptop2(conn net.Conn, rip string, psw string) { //执行转发
	defer recover()
	conn2, err := net.Dial("tcp", rip)
	if err != nil {
		fmt.Println("conn2 err" + err.Error())
		conn.Close()
		return
	}

	go Atob(conn, conn2, psw)
	go Atob(conn2, conn, psw)
}

func (this *mpsinfo) ptop() { //端口转发服务
	go func() {
		defer fmt.Println("端口转发退出:", this.rip)
		defer delete(mpstab, this.id)
		defer recover()
		//defer this.listener.Close()

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
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
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
		go Atob(conn, conn2, "")
		go Atob(conn2, conn, "")

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
		go Atob(conn, conn2, "")
		go Atob(conn2, conn, "")

	default:
		conn.Close()
	}
}

func socksswich(conn net.Conn) { //判断代理类型
	defer recover()
	bufab := make([]byte, RECV_BUF_LEN)
	conn.SetDeadline(time.Now().Add(CONNTO_MAX))
	n, err := conn.Read(bufab)
	if bytes.Equal(bufab[0:2], []byte{5, 1}) && err == nil { //socks5
		s5(conn, n)
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

func iniloadln(r *bufio.Reader) string {
	str, _ := r.ReadString('\n')
	str = strings.Trim(str, "\r\n")
	return str
}

func Atob(conn, conn2 net.Conn, psw string) { //数据转发
	bufab := make([]byte, RECV_BUF_LEN)
	defer recover()
	defer conn.Close()
	defer conn2.Close()
	defer func() {
		abnumad <- false
	}()
	pswlen := len(psw)
	j := 0
	abnumad <- true

	for notquit {

		conn.SetDeadline(time.Now().Add(CONNTO_MAX))
		n, err := conn.Read(bufab)

		if n <= 0 || err != nil {
			return
		}
		if psw != "" {
			for i := 0; i < n; i++ {
				bufab[i] ^= psw[j]
				j++
				if j >= pswlen {
					j = 0
				}
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

func xor(str, psw string) string { //异或加密
	pswlen := len(psw)
	j := 0
	n := len(str)
	buf := []byte(str)
	for i := 0; i < n; i++ {
		buf[i] ^= psw[j]
		j++
		if j >= pswlen {
			j = 0
		}
	}

	return string(buf)
}
