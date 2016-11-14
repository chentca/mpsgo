package main

//ver 3.6.4 2016.11.14
import (
    "bufio"
    "flag"
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
const RECV_BUF_LEN = 20480
const hideflag bool = false
const CONNTO_MIN = time.Second * 10
const CONNTO_MID = time.Minute * 2
const CONNTO_MAX = time.Hour * 2
const DialTO = time.Second * 5

var strlist0 []string = []string{
    "0:帮助，help;Telnet 0 to quit.",
    "1:MPS运行列表,MPS list",
    "2:停止一项MPS服务,Stop a MPS",
    "3:端口转发,Port to Port",
    "4:socks5代理,Sock5",
    "5:远程端口转发服务,MPS svr",
    "6:远程端口资源（反向连接资源）,MPS source",
    "7:远程端口用户,MPS user",
    "8:运行状态信息,info",
    "9:设置,set",
    "r:资源中继,MPS source relay",
    "b:资源桥,MPS source bridge",
    "t:Telnet服务,Telnet"}

/*
,
    "10:UTU A:UDP转TCP,UTU A:UDP to TCP",
    "11:UTU B:TCP转UDP,UTU B:TCP to UDP",
    "12:TUT A:TCP转UDP,TUT A:TCP to UDP",
    "13:TUT B:UDP转TCP,TUT B:UDP to TCP"

*/

var strtab0 []string = []string{"1:切换运行状态（自动保存恢复配置/手动保存恢复配置）",
    "2:读取配置并执行",
    "3:保存配置",
    "任意键:返回主菜单"}

var dbgflag bool = false
var Numcpu int = runtime.NumCPU()
var mpsini os.File
var autorun = "0"
var okbyte = []byte{10}
var okbyte1 = []byte{11}
var quitcode = []byte{1}
var notquit bool = true
var mpsid, abnum, abwaitnum int = 0, 0, 0
var reads int64 = 0
var spd1, spd10, spd60 int64 = 0, 0, 0
var req chan int = make(chan int, 100)       //统计转发数量
var abnumad chan bool = make(chan bool, 100) //统计转发线程数量
var abwait chan bool = make(chan bool, 100)  //统计转发线程空闲数量
var mpstab map[int]*mpsinfo = make(map[int]*mpsinfo)
var mpssvrtab map[string]*map[string]*[]net.Conn = make(map[string]*map[string]*[]net.Conn)
var mpsdone map[string]int = make(map[string]int)
var strlist1, strtab string
var uttab map[string]*uttabdata = make(map[string]*uttabdata) //建立ut的对应关系
var abfchan chan bool = make(chan bool, 100)                  //控制重复使用的转发纤程“空闲”上限
var abdatechan chan *abdate = make(chan *abdate, 100)         //记录需要转发的数据
var spdchan10 chan int64 = make(chan int64, 10)               //用于计算网速
var spdchan600 chan int64 = make(chan int64, 600)

type abdate struct {
    conn  net.Conn
    conn2 net.Conn
    psw   string
}

type tabreq struct {
    i int
    c net.Conn
}

type mpsudp struct {
    indatareq, outdatareq, udpans chan []byte
    tutidreq                      chan byte
    udpconn                       *net.UDPConn
    udpaddr                       *net.UDPAddr
    conn                          net.Conn
    tuttab                        *map[int]net.Conn
    mpsinfo                       *mpsinfo
}

type utdata struct {
    udplistener *net.UDPConn
    udpaddr     *net.UDPAddr
    buf         []byte
    this        *mpsinfo
}

type uttabdata struct {
    conn     net.Conn
    id       byte
    tutidreq chan byte
    bufreq   chan []byte
}

type mpsinfo struct { //mps信息记录
    info, lip, rip, mpsname, psw string
    ftype, id                    int
    listener                     net.Listener
    running                      bool
    udplistener                  *net.UDPConn
}

type callback func(this *mpsinfo, conn net.Conn)

func main() {
    //初始化网速计算变量
    for i := 0; i < 9; i++ {
        spdchan10 <- 0
    }
    for i := 0; i < 599; i++ {
        spdchan600 <- 0
    }

    flag.Parse() //读取命令行
    dbgflag = flag.Arg(0) == "dbg"

    if dbgflag {
        dbg("dbg on!")
    }
    strlist1 = stradd(strlist0)
    strtab = stradd(strtab0)
    defer quiter()
    defer recover()
    var n int
    var spdn int64 = 0
    runtime.GOMAXPROCS(Numcpu)

    ini, err := os.OpenFile("mpsgo.ini", os.O_RDWR|os.O_CREATE, 0666)
    if err != nil {
        dbg("err=", err.Error())
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

    if !hideflag {
        go inputer(nil, &mpsinfo{running: true})
        fmt.Println(strlist1)
    }
    var f, w bool
    after := time.After(time.Second)

    for notquit {
        select {
        case <-after: //计算单位时间的传输速度
            spd1 = reads - spdn
            spdn = reads
            spdchan10 <- spd1
            spdchan600 <- spd1
            hisspd := <-spdchan10 //只能取出来用，如果放在运算中会出现异常，chan持续减少直至锁死
            spd10 = (spd10*10 - hisspd + spd1) / 10
            if spd10 < 0 {
                spd10 = 0
            }
            hisspd = <-spdchan600
            spd60 = (spd60*600 - hisspd + spd1) / 600
            if spd60 < 0 {
                spd60 = 0
            }
            after = time.After(time.Second)

        case n = <-req: //统计转发数据量
            reads += int64(n)

        case f = <-abnumad: //统计转发纤程数量
            if f == true {
                abnum++
            } else {
                abnum--
            }
        case w = <-abwait: //统计空闲纤程数量
            if w == true {
                abwaitnum++
            } else {
                abwaitnum--
            }
        }
    }

}

func mpssource2(this *mpsinfo, conn net.Conn) { //mps的资源
    buf := make([]byte, 1)
    conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
    conn.Read(buf)
    if buf[0] != 10 {
        conn.Close()
        time.Sleep(time.Second)
        return
    }
    conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    conn.Read(buf)
    if buf[0] != 11 {
        conn.Close()
        time.Sleep(time.Second)
        return
    }
    conn2, err := net.DialTimeout("tcp", this.lip, DialTO)
    if err == nil {
        Atob(conn, conn2, this.mpsname)
        Atob(conn2, conn, this.mpsname)
    } else {
        conn.Close()
        time.Sleep(time.Second)
    }
}

func mpssvr21(conn net.Conn, mpsname string) { //资源握手判断
    value, ok := mpssvrtab[mpsname]
    if !ok {
        source := new([]net.Conn)
        user := new([]net.Conn)
        value = &map[string]*([]net.Conn){"s": source, "u": user}
        mpssvrtab[mpsname] = value
    }
    source := (*value)["s"]
    user := (*value)["u"]
    *source = append(*source, conn)
    if len(*source) > 1 {
        conn = (*source)[0]
        *source = (*source)[1:]
        //conn.Write(quitcode)
        conn.Close()
    }
    if len(*user) > 0 {
        hand(source, user, mpsname)
    }
}

func mpssvr22(conn net.Conn, mpsname string) { //用户握手判断
    value, ok := mpssvrtab[mpsname]
    if !ok {
        source := new([]net.Conn)
        user := new([]net.Conn)
        value = &map[string]*([]net.Conn){"s": source, "u": user}
        mpssvrtab[mpsname] = value
    }
    source := (*value)["s"]
    user := (*value)["u"]
    *user = append(*user, conn)
    if len(*user) > 60 {
        conn = (*user)[0]
        *user = (*user)[1:]
        //conn.Write(quitcode)
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
    conn.Read(buf)
    if buf[0] != 10 {
        conn.Close()
        time.Sleep(time.Second)
        return
    }
    conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    conn.Read(buf)
    if buf[0] != 11 {
        conn.Close()
        time.Sleep(time.Second)
        return
    }

    //当作用户判断握手
    value, ok := mpssvrtab[this.mpsname]
    if !ok {
        source := new([]net.Conn)
        user := new([]net.Conn)
        value = &map[string]*([]net.Conn){"s": source, "u": user}
        mpssvrtab[this.mpsname] = value
    }
    source := (*value)["s"]

    if len(*source) > 0 {
        var conn2 = (*source)[0]
        if len(*source) == 1 {
            *source = []net.Conn{}
        } else {
            *source = (*source)[1:]
        }
        conn2.Write(okbyte1)
        Atob(conn, conn2, "")
        Atob(conn2, conn, "")

        mpsdone[this.mpsname]++
    } else {
        //conn.Write(quitcode)
        conn.Close()
        time.Sleep(time.Second)
    }

}

func mpsbridge2(this *mpsinfo, conn net.Conn) { //当作mps的资源
    dbg("send source:", this.info)
    buf := make([]byte, 1)
    conn.Write(append([]byte{1}, []byte(this.mpsname)...)) //type source
    conn.Read(buf)
    if buf[0] != 10 {
        conn.Close()
        time.Sleep(time.Second)
        return
    }
    conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    conn.Read(buf)
    if buf[0] != 11 {
        conn.Close()
        time.Sleep(time.Second)
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
    conn2.SetDeadline(time.Now().Add(CONNTO_MIN))
    conn2.Read(buf)
    if buf[0] != 10 {
        conn.Close()
        conn2.Close()
        time.Sleep(time.Second)
        return
    }
    conn2.Read(buf)
    if buf[0] != 11 {
        conn.Close()
        conn2.Close()
        time.Sleep(time.Second)
        return
    }
    Atob(conn, conn2, "")
    Atob(conn2, conn, "")
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

    conn2.SetDeadline(time.Now().Add(CONNTO_MIN))
    _, err = conn2.Read(buf)
    if err != nil || buf[0] != 10 {
        conn.Close()
        conn2.Close()
        return
    }
    _, err = conn2.Read(buf)
    if err != nil || buf[0] != 11 {
        conn.Close()
        conn2.Close()
        return
    }
    Atob(conn, conn2, this.mpsname)
    Atob(conn2, conn, this.mpsname)

}

func mpsone(this *mpsinfo, next callback) {
    var conn net.Conn
    var err error
    var retrynum int
    for this.running {
        retrynum = 1
        for this.running {
            dbg("send source")
            conn, err = net.DialTimeout("tcp", this.rip, DialTO) //发出资源
            if err == nil {
                break
            }

            for i := 1; i < retrynum; i++ {
                time.Sleep(time.Second * 5)
            }
            if retrynum < 12 {
                retrynum++
            }
        }
        conn.SetDeadline(time.Now().Add(CONNTO_MID))
        next(this, conn)
    }

}

func (this *mpsinfo) mpsuser() { //mps资源使用者
    go func() {
        defer dbg("MPS用户退出:", this.rip)
        defer recover()
        defer delete(mpstab, this.id)
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
    conn.Write(okbyte1)
    conn2.Write(okbyte1)

    Atob(conn, conn2, "")
    Atob(conn2, conn, "")

    mpsdone[mpsname]++
}

func mpssvr2(conn net.Conn) {
    defer recover()
    var bufab = make([]byte, 200)
    //conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    n, err := conn.Read(bufab)
    if err != nil {
        conn.Close()
        return
    }
    mpsname := string(bufab[1:n])
    switch bufab[0] { //第一次握手
    case 1: //source
        conn.Write(okbyte)
        mpssvr21(conn, mpsname)

    case 2: //user
        conn.Write(okbyte)
        mpssvr22(conn, mpsname)

    default:
        conn.Close()
    }
}

func (this *mpsinfo) mpssvr() { //mps中转服务
    go func() {
        defer dbg("MPS服务退出:", this.id)
        defer delete(mpstab, this.id)
        defer recover()

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
        if !hideflag {
            fmt.Println(str)
        }
    } else {
        conn.Write([]byte(str))
    }
}

func inputer(conn net.Conn, this *mpsinfo) { //cmd交互界面
    if conn != nil {
        pswipt := telsvrinputer("psw:", conn) //验证密码
        if pswipt != this.psw {
            conn.Close()
            return
        }
    }
    var inputstr string
    var inputint int
    for notquit && this.running {
        inputstr = inputerin("", conn)
        if conn != nil {
            conn.SetDeadline(time.Now().Add(CONNTO_MID))
        }
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
            case 3, 4, 5, 7, 8, 9, 10:
                mpstab[inputint].listener.Close()
            case 6: //mps资源？

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
            rip = inputerin("请输入转发端口(如：1.2.3.4:80 或 1.2.3.4:80;1.2.3.4:82)*取消:", conn)

            if rip == "*" || len(rip) == 0 {
                continue
            }
            psw := inputerin("转发密码或干扰码(密码使用任意英文字符)*取消:", conn)

            if psw == "*" {
                continue
            }
            if psw == " " {
                psw = ""
            }
            var listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                inputerout("添加端口转发失败:"+err.Error(), conn)
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + " psw:" + psw, lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
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
            psw := inputerin("连接密码（*取消:）", conn)
            if psw == "*" {
                continue
            }
            var listener, err = net.Listen("tcp", lip)
            if err != nil {
                inputerout("启动Telsvr失败:"+err.Error(), conn)
                continue
            }

            mpsid++
            mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid, psw: psw, running: true}
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
        case "11": //utut2u type8
            var lip, rip string
            lip = inputerin("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

            if lip == "*" || len(lip) == 0 {
                continue
            }
            rip = inputerin("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

            if rip == "*" || len(rip) == 0 {
                continue
            }

            var listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                inputerout("添加utut2u失败:"+err.Error(), conn)
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 8, info: lip + "utut2u->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].ututu()
            inputerout(mpstab[mpsid].info, conn)
            inputerout(list(), conn)
        case "10": //UTU-U2T type9
            var lip, rip string
            lip = inputerin("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

            if lip == "*" || len(lip) == 0 {
                continue
            }
            rip = inputerin("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

            if rip == "*" || len(rip) == 0 {
                continue
            }

            var listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                inputerout("添加UTU-U2T失败:"+err.Error(), conn)
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 9, info: lip + "UTU-U2T->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].utuut()
            inputerout(mpstab[mpsid].info, conn)
            inputerout(list(), conn)

        case "12": //TUT-T2U
            var lip, rip string
            lip = inputerin("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

            if lip == "*" || len(lip) == 0 {
                continue
            }
            rip = inputerin("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

            if rip == "*" || len(rip) == 0 {
                continue
            }

            var listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                inputerout("添加TUT-T2U失败:"+err.Error(), conn)
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 13, info: lip + "TUT TU->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].tuttu()
            inputerout(mpstab[mpsid].info, conn)
            inputerout(list(), conn)

        case "13": //TUT-U2T
            var lip, rip string
            lip = inputerin("请输入本地端口(如：127.0.0.1:80 或 0.0.0.0:80)*取消:", conn)

            if lip == "*" || len(lip) == 0 {
                continue
            }
            rip = inputerin("请输入转发端口(如：123.0.0.123:80 或 www.xxx.com:80)*取消:", conn)

            if rip == "*" || len(rip) == 0 {
                continue
            }
            udpaddr, err := net.ResolveUDPAddr("udp4", lip)
            if err != nil {
                conn.Close()
                return
            }
            udplistener, err := net.ListenUDP("udp4", udpaddr) //侦听端口
            if err != nil {
                inputerout("添加TUT-U2T失败:"+err.Error(), conn)
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{udplistener: udplistener, ftype: 14, info: lip + "TUT UT->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].tutut()
            inputerout(mpstab[mpsid].info, conn)
            inputerout(list(), conn)

        case "0":
            if conn == nil {
                inputerout(strlist1, conn)
                continue
            }
            conn.Close()
            return

        default: //help
            /*if conn != nil && inputstr == "0" {
                return
            }
            inputstr = "0"*/
            inputerout(strlist1, conn)
        }
    }
}

func (this *mpsinfo) telsvr() { //TelSvr服务
    go func() {
        defer dbg("TelSvr退出:", this.id)
        defer delete(mpstab, this.id)
        defer recover()

        for notquit && this.running {
            conn, err := this.listener.Accept() //接受连接
            if err != nil {
                dbg("TelSvr接收错误", err)
                return
            }

            dbg("TelSvr接收新连接。", conn.RemoteAddr())
            go inputer(conn, this)
        }
    }()
}

func state() string {
    var str string
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    str = "状态信息:\r\n"
    str += fmt.Sprint(" NumCPU: ", Numcpu, " 转发纤程: ", abnum, " 空闲:", abwaitnum, "\r\n")
    str += fmt.Sprint(" 转发数据: ", reads, " 速度：秒-10秒-10分钟 ", spd1, spd10, spd60, "\r\n")
    for k, v := range mpssvrtab {
        str += fmt.Sprint("mpsname:", k, " 资源:", strconv.Itoa(len(*(*v)["s"])), " 用户:", strconv.Itoa(len(*(*v)["u"])), " 对接:", mpsdone[k], "\r\n")
    }
    str += fmt.Sprint("bufchan:", len(bufchan), " atob chan:", len(abfchan), "\r\n")
    str += fmt.Sprint("HeapSys/程序向应用程序申请的内存:", m.HeapSys, " HeapAlloc/堆上目前分配的内存:", m.HeapAlloc, "\r\n")
    str += fmt.Sprint("HeapIdle/堆上目前没有使用的内存:", m.HeapIdle, " HeapReleased/回收到操作系统的内存:", m.HeapReleased, "\r\n")
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
        switch v.ftype {
        case 3, 6, 7, 8, 9, 11, 12, 13, 14:
            wstr(ini, v.rip)
        }
        if v.ftype == 3 || v.ftype == 10 {
            wstr(ini, v.psw)
        }
        switch v.ftype {
        case 6, 7, 11, 12:
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
            if psw == " " {
                psw = ""
            }

            listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                fmt.Println("添加端口转发失败:" + err.Error())
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 3, info: lip + "->" + rip + " psw:" + psw, lip: lip, rip: rip, psw: psw, id: mpsid, running: true}
            mpstab[mpsid].ptop()
            dbg(mpstab[mpsid].info)

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
            dbg(mpstab[mpsid].info)

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
            dbg(mpstab[mpsid].info)

        case "6":
            lip := iniloadln(r)
            rip := iniloadln(r)
            mpsname := iniloadln(r)
            mpsid++
            mpstab[mpsid] = &mpsinfo{lip: lip, rip: rip, ftype: 6, info: " MPS资源: " + lip + " mpsname:[" + mpsname + "]" + " 连接到: " + rip, id: mpsid, running: true, mpsname: mpsname}
            mpstab[mpsid].mpssource()
            dbg(mpstab[mpsid].info)

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
            dbg(mpstab[mpsid].info)
        case "8":
            lip := iniloadln(r)
            rip := iniloadln(r)

            listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                fmt.Println("添加UTU TU失败:" + err.Error())
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 8, info: lip + "UTU TU->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].ututu()
            dbg(mpstab[mpsid].info)
        case "9":
            lip := iniloadln(r)
            rip := iniloadln(r)

            listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                fmt.Println("添加UTU UT失败:" + err.Error())
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 9, info: lip + "UTU UT->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].utuut()
            dbg(mpstab[mpsid].info)
        case "10":
            lip := iniloadln(r)
            psw := iniloadln(r)
            if psw == " " {
                psw = ""
            }
            listener, err = net.Listen("tcp", lip)
            if err != nil {
                fmt.Println("启动Telsvr服务失败:" + err.Error())
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{lip: lip, listener: listener, ftype: 10, info: " TelSvr服务: " + lip, id: mpsid, psw: psw, running: true}
            mpstab[mpsid].telsvr()
            dbg(mpstab[mpsid].info)

        case "11":
            _ = iniloadln(r)
            rip := iniloadln(r)
            mpsname := iniloadln(r)

            mpsid++
            mpstab[mpsid] = &mpsinfo{ftype: 11, lip: "0", info: " MPS中继服务:[" + mpsname + "]->" + rip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
            mpstab[mpsid].mpsrelay()
            dbg(mpstab[mpsid].info)

        case "12":
            lip := iniloadln(r)
            rip := iniloadln(r)
            mpsname := iniloadln(r)

            mpsid++
            mpstab[mpsid] = &mpsinfo{ftype: 12, info: " MPS服务桥: " + rip + "[" + mpsname + "]->" + lip, lip: lip, rip: rip, id: mpsid, mpsname: mpsname, running: true}
            mpstab[mpsid].mpsbridge()
            dbg(mpstab[mpsid].info)

        case "13":
            lip := iniloadln(r)
            rip := iniloadln(r)

            listener, err = net.Listen("tcp", lip) //侦听端口
            if err != nil {
                fmt.Println("添加TUT TU失败:" + err.Error())
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{listener: listener, ftype: 13, info: lip + "TUT TU->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].tuttu()
            dbg(mpstab[mpsid].info)
        case "14":
            lip := iniloadln(r)
            rip := iniloadln(r)
            udpaddr, err := net.ResolveUDPAddr("udp4", lip)
            if err != nil {
                continue
            }
            udplistener, err := net.ListenUDP("udp4", udpaddr) //侦听端口
            if err != nil {
                continue
            }
            mpsid++
            mpstab[mpsid] = &mpsinfo{udplistener: udplistener, ftype: 14, info: lip + "TUT UT->" + rip, lip: lip, rip: rip, id: mpsid, running: true}
            mpstab[mpsid].tutut()
            dbg(mpstab[mpsid].info)
        }
    }
    //fmt.Print(list())
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
    dbg("Recovered")
}

func ptop2(connchan chan net.Conn, rip string, psw string) { //执行转发
    defer recover()
    for conn := range connchan {
        dbg("PtoP will dial to:", rip)
        conn2, err := net.Dial("tcp", rip)
        if err != nil {
            fmt.Println("conn2 err" + err.Error())
            conn.Close()
            continue
        }
        Atob(conn, conn2, psw)
        Atob(conn2, conn, psw)
    }
}

func (this *mpsinfo) ptop() { //端口转发服务
    var connchan chan net.Conn = make(chan net.Conn, 10)
    var riplst []string = strings.Split(this.rip, ";")
    for _, rip := range riplst {
        go ptop2(connchan, rip, this.psw)
    }
    go func() {
        defer dbg("端口转发退出:", this.rip)
        defer delete(mpstab, this.id)
        defer close(connchan)
        defer recover()

        for notquit && this.running {
            conn, err := this.listener.Accept() //接受连接
            if err != nil {
                return
            }
            connchan <- conn
        }
    }()
}

func s5(conn net.Conn, bufab []byte) {
    defer recover()
    n, err := conn.Write([]byte{5, 0})
    if err != nil {
        conn.Close()
        return
    }
    n, err = conn.Read(bufab)
    if n == 0 || err != nil {
        conn.Close()
        return
    }

    switch {
    case string(bufab[0:4]) == string([]byte{5, 1, 0, 1}): //ip请求
        ip := fmt.Sprintf("%d.%d.%d.%d:%d", bufab[4], bufab[5], bufab[6], bufab[7], int(bufab[8])<<8+int(bufab[9]))
        if bufab[4] == 0 {
            conn.Close()
            return
        }
        conn2, err := net.DialTimeout("tcp", ip, DialTO)
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
        Atob(conn, conn2, "")
        Atob(conn2, conn, "")

    case string(bufab[0:4]) == string([]byte{5, 1, 0, 3}): //域名请求
        ip := fmt.Sprintf("%s:%d", string(bufab[5:bufab[4]+5]), int(bufab[n-2])<<8+int(bufab[n-1]))
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
        Atob(conn, conn2, "")
        Atob(conn2, conn, "")

    default:
        conn.Close()
    }
}

func socksswich(connchan chan net.Conn, this *mpsinfo) { //判断代理类型
    defer recover()
    bufab := make([]byte, 200)
    for notquit && this.running {
        conn := <-connchan
        conn.SetDeadline(time.Now().Add(CONNTO_MIN))
        _, err := conn.Read(bufab)
        if string(bufab[0:2]) == string([]byte{5, 1}) && err == nil { //socks5
            s5(conn, bufab)
            continue
        }
        conn.Close()
    }
}

func (this *mpsinfo) socks45() {
    connchan := make(chan net.Conn, 500)
    go socksswich(connchan, this)
    go func() {
        defer dbg("socks服务器退出")
        defer delete(mpstab, this.id)
        defer recover()

        for notquit && this.running {
            conn, err := this.listener.Accept() //接受连接
            if err != nil {
                dbg("接受代理错误1：" + err.Error())
                this.running = false
                return
            }
            connchan <- conn
        }
    }()
}

func iniloadln(r *bufio.Reader) string {
    str, _ := r.ReadString('\n')
    str = strings.Trim(str, "\r\n")
    return str
}

func (this *mpsinfo) ututu() {
    go func() {
        defer dbg("TCPtoUDP退出:", this.rip)
        defer delete(mpstab, this.id)
        defer recover()

        for notquit && this.running {
            conn, err := this.listener.Accept() //接受连接
            if err != nil {
                return
            }
            go ututu1(conn, this.rip)
        }
    }()
}

func (this *mpsinfo) utuut() {
    var utreq chan utdata = make(chan utdata, 100)
    go func(this *mpsinfo, utreq chan utdata) {
        for notquit && this.running {
            select {
            case u2tdata := <-utreq:
                id := u2tdata.udpaddr.String()
                value, ok := uttab[id]
                if !ok {
                    conn2, err := net.DialTimeout("tcp", this.rip, time.Second)
                    if err != nil {
                        fmt.Println("utuu2t conn2 err" + err.Error())
                        continue
                    }
                    tutidreq := make(chan byte)
                    value = &uttabdata{conn: conn2, id: 0, tutidreq: tutidreq}
                    uttab[id] = value
                }
                conn2 := (*value).conn
                _, err := conn2.Write(u2tdata.buf)
                if err != nil {
                    conn2.Close()
                    delete(uttab, id)
                    continue
                }
                go utuut1(u2tdata.udplistener, u2tdata.udpaddr, conn2)
            }
        }
    }(this, utreq)
    go uttoreq(this, utreq)
}

func ututu1(conn net.Conn, rip string) { //执行转发
    defer recover()
    udpaddr, err := net.ResolveUDPAddr("udp4", rip)
    if err != nil {
        conn.Close()
        return
    }

    //udp连接
    udpConn, err := net.DialUDP("udp4", nil, udpaddr)
    if err != nil {
        conn.Close()
        return
    }
    Atob(conn, udpConn, "")
    Atob(udpConn, conn, "")
}

func utuut1(conn1 *net.UDPConn, udpaddr *net.UDPAddr, conn2 net.Conn) { //utu ut back
    bufab := make([]byte, RECV_BUF_LEN)
    defer recover()
    defer func() {
        abnumad <- false
        conn2.Close()
    }()
    abnumad <- true
    for notquit {
        //conn2.SetDeadline(time.Now().Add(CONNTO_MAX))
        n, err := conn2.Read(bufab)

        if n <= 0 || err != nil {
            return
        }
        n, err = conn1.WriteToUDP(bufab[:n], udpaddr)
        if n <= 0 || err != nil {
            return
        }
        req <- n
    }
}

func workerget() (net.Conn, net.Conn, string) {
    select {
    case labdate := <-abdatechan:
        return labdate.conn, labdate.conn2, labdate.psw
    case <-time.After(time.Minute * 30):
        return nil, nil, ""
    }

}

var bufchan chan []byte = make(chan []byte, 100)

func bufout() []byte {
    var buf []byte
    select {
    case buf = <-bufchan:
    default:
        buf = make([]byte, RECV_BUF_LEN)
    }
    return buf
}

func bufin(buf []byte) {
    select {
    case bufchan <- buf:
    default:
        go func() {
            select {
            case bufchan <- buf:
            case <-time.After(time.Minute * 30):
            }
        }()
    }
}

func Atob(conn, conn2 net.Conn, psw string) {
    conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    conn2.SetDeadline(time.Now().Add(CONNTO_MAX))
    var newabdate *abdate
    newabdate = new(abdate)
    newabdate.conn = conn
    newabdate.conn2 = conn2
    newabdate.psw = psw
    abdatechan <- newabdate
    select {
    case <-abfchan:
    default:
        go Atobf()
    }
}

type da struct {
    conn, conn2 net.Conn
    buf         []byte
    n           int
}

func Atobf() { //数据转发
    abnumad <- true
    var conn net.Conn
    var conn2 net.Conn
    var dachan chan da
    var psw string
    var pswlen, j int
    defer recover()
    defer func() {
        abnumad <- false
        <-abfchan
        abwait <- false //接收
        abwait <- false //发送
        close(dachan)
    }()
    dachan = make(chan da, 10)
    go Atobf2(dachan)
rst:
    abwait <- true //发送
    abwait <- true //发送
    conn, conn2, psw = workerget()
    if conn == nil {
        return
    }
    pswlen = len(psw)
    j = 0 //加密位
    dachan <- da{conn: conn, conn2: conn2}
    abwait <- false //发送
    abwait <- false //发送

    for notquit {
        bufab := bufout()
        n, err := conn.Read(bufab)
        if n <= 0 || err != nil {
            dachan <- da{buf: bufab, n: 0}
            conn.Close()
            abfchan <- true //进入调用队列
            goto rst
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

        dachan <- da{buf: bufab, n: n}

        conn.SetDeadline(time.Now().Add(CONNTO_MAX))
        conn2.SetDeadline(time.Now().Add(CONNTO_MAX))
    }

}

func Atobf2(dachan chan da) { //数据转发
    abnumad <- true
    defer func() {
        abnumad <- false
    }()

rst:
    da := <-dachan
    conn := da.conn
    conn2 := da.conn2

    for da = range dachan {
        if da.n == 0 {
            bufin(da.buf)
            conn2.Close()
            goto rst
        }
        n, err := conn2.Write(da.buf[0:da.n])
        if n <= 0 || err != nil {
            conn.Close()
            continue
        }
        req <- n
        bufin(da.buf)
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

func (this *mpsinfo) tuttu() {
    go func() {
        defer dbg("TCPtoUDP退出:", this.rip)
        defer delete(mpstab, this.id)
        defer recover()
        tuttab := make(map[int]net.Conn)
        treq := make(chan tabreq)
        //udp拨号
        udpaddr, err := net.ResolveUDPAddr("udp4", this.rip)
        if err != nil {
            this.listener.Close()
            dbg("TUT-TU quit:", err)
            return
        }
        conn2, err := net.DialUDP("udp4", nil, udpaddr)
        if err != nil {
            this.listener.Close()
            dbg("TUT-TU quit:", err)
            return
        }
        //初始化mpsudp
        mpsudpa := mpsudp{udpconn: conn2, tuttab: &tuttab, mpsinfo: this}
        mpsudpa.run()

        go ut1(this, &mpsudpa, treq)
        go treqf(treq, &tuttab)
        var connid int = 1
        for notquit && this.running {
            conn, err := this.listener.Accept()
            if err != nil {
                dbg("accept err", this.info)
                continue
            }

            for _, ok := tuttab[connid]; ok; {
                if connid > 65535 {
                    connid = 1
                } else {
                    connid++
                }
            }
            tuttab[connid] = conn
            go tu1(connid, conn, this, &mpsudpa, treq)
            if connid > 65535 {
                connid = 1
            } else {
                connid++
            }
            dbg("connid:", connid)
        }
    }()
}

func treqf(treq chan tabreq, tuttab *map[int]net.Conn) {
    v := <-treq
    if v.c != nil {
        (*tuttab)[v.i] = v.c
    } else {
        delete(*tuttab, v.i)
    }
}

func tu1(connid int, conn net.Conn, mpsinfoa *mpsinfo, mpsudpa *mpsudp, treq chan tabreq) { //处理Tcp转udp
    buf := make([]byte, RECV_BUF_LEN-3) //为udp接收长度留出空间
    idbuf := itob(connid)
    defer func() {
        //indatareq <- idbuf
        treq <- tabreq{i: connid}
        //dbg("tu quit", connid, mpsudpa.tuttab)
        conn.Close()
    }()
    for notquit && mpsinfoa.running {
        //conn.SetDeadline(time.Now().Add(CONNTO_MAX))
        n, err := conn.Read(buf)
        if err != nil || n <= 0 {
            return
        }
        bufa := append(idbuf, buf[:n]...)
        mpsudpa.indatareq <- bufa //输入mpsudp的接收入口
    }
}

func ut1(mpsinfoa *mpsinfo, mpsudpa *mpsudp, treq chan tabreq) { //ut
    var connid int
    defer func() {
        treq <- tabreq{i: connid}
    }()
    for notquit && mpsinfoa.running {
        buf := <-mpsudpa.outdatareq //从mpsudp输出口取得
        connid = int(buf[0])<<8 + int(buf[1])
        conn, ok := (*mpsudpa.tuttab)[connid]
        if !ok {
            dbg("tut tu 无对应conn", connid)
            continue
        }
        _, err := conn.Write(buf[2:])
        if err != nil {
            conn.Close()
            return
        }
        //conn.SetDeadline(time.Now().Add(CONNTO_MAX))
    }
}

func (this *mpsinfo) tutut() {
    go func() {
        tuttab := make(map[int]net.Conn)
        treq := make(chan tabreq)

        udpaddr := new(net.UDPAddr)
        mpsudpa := mpsudp{udpaddr: udpaddr, udpconn: this.udplistener, tuttab: &tuttab, mpsinfo: this}
        _, outdata := mpsudpa.run()

        go treqf(treq, &tuttab)
        for notquit && this.running {
            buf := <-outdata //ut
            connid := int(buf[0])<<8 + int(buf[1])
            conn, ok := tuttab[connid]
            var err error
            if !ok {
                conn, err = net.DialTimeout("tcp", this.rip, DialTO)
                if err != nil {
                    continue
                }
                //conn.SetDeadline(time.Now().Add(CONNTO_MAX))
                tuttab[connid] = conn
                go tu1(connid, conn, this, &mpsudpa, treq)
            }
            conn.Write(buf[2:]) //ut
        }
    }()
}

func uttoreq(this *mpsinfo, utreq chan utdata) {
    defer dbg("UDPtoTCP退出:", this.rip)
    defer delete(mpstab, this.id)
    defer recover()
    //监听地址
    udpaddr, err := net.ResolveUDPAddr("udp4", this.lip)
    if err != nil {
        return
    }
    //监听连接
    udpListener, err := net.ListenUDP("udp4", udpaddr)
    if err != nil {
        return
    }
    buf := make([]byte, RECV_BUF_LEN)

    for notquit && this.running {
        n, udpaddr, err := udpListener.ReadFromUDP(buf)
        if err != nil {
            continue
        }
        utreq <- utdata{udplistener: udpListener, udpaddr: udpaddr, buf: buf[:n], this: this}
    }
}

func (this *mpsudp) run() (indata chan []byte, outdata chan []byte) {
    this.indatareq = make(chan []byte)
    this.outdatareq = make(chan []byte)
    this.tutidreq = make(chan byte)
    this.udpans = make(chan []byte)

    go func() { //udpread
        bufa := make([]byte, RECV_BUF_LEN+256)
        defer recover()
        var id byte = 255
        for notquit && this.mpsinfo.running {
            //this.udpconn.SetDeadline(time.Now().Add(CONNTO_MAX))
            n, udpaddr, err := this.udpconn.ReadFromUDP(bufa)
            if n <= 0 || err != nil {
                continue
            }
            if this.udpaddr != nil {
                this.udpaddr = udpaddr
            }
            if n == 1 {
                this.tutidreq <- bufa[0]
                continue //收到反馈
            }

            if id == bufa[0] {
                dbg("udp 重复信息丢弃。", id, bufa[:10])
                this.udpans <- []byte{bufa[0]}
                continue
            }
            id = bufa[0]
            this.outdatareq <- bufa[1:n]
            this.udpans <- []byte{bufa[0]}
        }
    }()

    go func() { //udpwrite
        var id, ida byte = 1, 1
        for notquit && this.mpsinfo.running {
            select {
            case ans := <-this.udpans:
                if this.udpaddr == nil {
                    this.udpconn.Write(ans)
                } else {
                    this.udpconn.WriteToUDP(ans, this.udpaddr)
                }
            case buf := <-this.indatareq:
                buf = append([]byte{id}, buf...)
                //dbg("udp write:", id)
                if this.udpaddr == nil {
                    this.udpconn.Write(buf)
                } else {
                    this.udpconn.WriteToUDP(buf, this.udpaddr)
                }
                after := time.After(time.Second)
                for i := 0; i < 5; {
                    select {
                    case ans := <-this.udpans:
                        if this.udpaddr == nil {
                            this.udpconn.Write(ans)
                        } else {
                            this.udpconn.WriteToUDP(ans, this.udpaddr)
                        }
                    case ida = <-this.tutidreq:
                        if ida == id {
                            i = 5
                            //dbg(id, "ok")
                        } else {
                            i++
                            dbg("udp received other one:", ida, id)
                            if this.udpaddr == nil {
                                this.udpconn.Write(buf)
                            } else {
                                this.udpconn.WriteToUDP(buf, this.udpaddr)
                            }
                        }
                    case <-after:
                        after = time.After(time.Millisecond * 10)
                        i++
                        //重发
                        //dbg("udp resend:", id)
                        if this.udpaddr == nil {
                            this.udpconn.Write(buf)
                        } else {
                            this.udpconn.WriteToUDP(buf, this.udpaddr)
                        }
                    }
                }
                if ida != id { //重试3次都不对
                    dbg("mpsudp write err!id,ida", id, ida)
                    //continue
                }
                if id == 255 {
                    id = 1
                } else {
                    id++
                }
                req <- len(buf) - 1
                //runtime.Gosched()
                time.Sleep(time.Microsecond)
            }
        }
    }()
    return this.indatareq, this.outdatareq
}
func itob(i int) []byte {
    a := i
    if i > 65535 {
        a = i - 65535
    }
    a1 := byte(a >> 8)
    a2 := byte(a - int(a1<<8))
    return []byte{a1, a2}
}
