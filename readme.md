MPSGO的设计目标：
基于端口连接的多种应用。
关键词：只要放开一个端口。。。

功能列表：
1.端口转发
2.端口转发加解密
3.socks代理
4.端口反向连接
5.配置文件管理，可实现配置的自动保存和加载

使用说明：
本软件用go语言实现，借由go语言的特点，以多线程构架，但go内部以协程的方式实现，稳定性和资源占用方面非常优异。
本软件仅实现了最基础的数据转发，不考虑加密及安全验证机制。仅提供了简单的异或加密，如需安全验证可在数据前端加入机制。

已知问题：
暂未实现gui或webui管理。

详细应用说明：
1.端口转发：
将一个端口的访问转向另一个端口。

2.加密的端口转发：
在端口转发的基础上增加了一个简单的加解密过程，加密和解密过程是一样的，密码不同则无法还原数据。
目的是绕过服务器简单的明文过滤。
比如你在家里开了个socks代理，在公司通过代理访问网址还是会被服务器过滤到，这时候可以用到它。
在公司开一个加密转发，本地端口为1080，数据转发到代理所在的ip比如homeip，端口为一个你可以访问到的端口，比如443。
家里需要也开一个加密转发，本地端口为443，转发端口为服务器端口，比如1080。
在公司使用127.0.0.1:1080就可以加密访问到家里的socks代理了。
加解密过程需要成对出现。


3.socks代理：
我只实现了socks5的tcp连接部分，udp部分未支持。


4.反向连接应用：这个是我开发这个软件的重点。思路来源是yyc的vidc软件，真是一代经典！向yyc致敬！
我重新写，并不是因为yyc的vidc不好用，而是因为它太好用了，太多人用于入侵，所以被各大杀毒软件禁了。
先解释什么叫反向连接。正常的连接就是我想连接某个ip:port，发出连接，使用连接。
反向连接是这样，我有一个端口，比如1080的socks服务端口，我可以主动把它提供给别人使用。
你可能会问，你开了我就用啊，还要你主动提供作甚？
由于各种原因，比如我的机子在内网，或者提供服务的机子的ip是动态的，别人无法直接访问到。
所以我需要把它发布到特定的地方，让别人可以访问到，并且使用到。
反向连接的功能涉及到3种连接服务：资源连接，中转服务，用户连接。
资源连接将端口资源发出给中转服务，用户连接从中转服务取得标识一样的资源连接并映射到本地端口，用户可以通过访问本地端口来连接并使用远程提供的端口资源。

端口-->资源连接-->中转服务-->用户连接-->端口


应用实例：我想从家里使用登录公司的oa网络
1.在公司我开启一个socks代理1080。
2.将代理端口通过MPS资源发送给家里的MPS服务的端口。（1080-->发到家里的MPS服务去了）
3.我回到家里的时候就可以开启MPS服务，再开启MPS用户连接从MPS服务获取这个MPS资源隐射到本地1080。我通过本地的1080端口的socks服务来达到和公司相同的网络环境可以访问oa。
（MPS服务为MPS资源和MPS用户提供数据中转。允许一个资源对应一个用户，也允许多对一和多对多。用户获得一个资源，映射到本地1080端口，那访问本地的1080端口就相当于访问到了公司的1080socks代理。）

4.如果我不在家，但是家里的中转服务开着。我同样可以在其他地方开启一个用户连接，从家里的中转服务取得这个资源连接，并且在异地访问公司oa。

5.如果你认为明文传送太不安全。可以在两段套上加密和解密的中转。加套的位置如下：

端口--(加套)-->资源连接-->中转服务-->用户连接--(解套)-->端口
也可以：
端口---->资源连接-->(加套)-->(解套)-->中转服务-->用户连接---->端口

但是不可以：
端口---->资源连接-(加套)->中转服务-(解套)->用户连接---->端口
因为加密后的数据无法被中转服务识别。

也可以多次加套，当然也得多次解套。
所有的转发都是有性能损失并且影响连接稳定性的。

实例说明：
比如，家里远程ip:110.1.1.1 公司ip：202.1.1.1 
我想在公司用家里的代理上网，并且数据要经过加密绕过网管的监视。
在家里：
1.选择4号功能，建立socks代理：127.0.0.1:1080家里就有一个1080的socks代理了，而这个代理只能本地访问。
4
127.0.0.1:1080
2.然后选择3号功能，建立加密通道，密码123：
2
0.0.0.0:110
127.0.0.1:1080
123

最终配置2条：
1.socks代理"127.0.0.1:1080"
2.端口转发"0.0.0.0:1000"->"127.0.0.1:1080" 密码123

在公司：
建立加密通道（加密和解密过程是一样的）。
1.选择3号功能：
127.0.0.1:1080
110.1.1.1:110
123说明，在本地建立一个1080的端口接收数据，加密后发往110.1.1.1的110端口。
最终配置1条：
1.端口转发"127.0.0.1:1080"->"110.1.1.1:110"密码123
在公司使用127.0.0.1:1080就可以加密代理出去了。

实例2：
如果我想在家里接入公司的网络（反向连接）：
在公司开个socks代理：
4
127.0.0.1:1080

然后6号功能,将代理端口作为资源发往MPS服务：
6
127.0.0.1:1080
110.1.1.1:110（也可以用动态域名）
officesvr

最终配置：
1.socks代理127.0.0.1:1080
2.MPS资源：127.0.0.1:1080发送到110.1.1.1:110 mpsname:officesvr

家里的配置：5号功能，开启MPS服务，接收MPS资源连接
5
0.0.0.0:110

7号功能获取MPS资源并绑定在本地端口使用
7
127.0.0.1:1080
127.0.0.1:110
officesvr

最终配置2条：
1.MPS服务0.0.0.0:110
2.MPS用户127.0.0.1:1080 发送到127.0.0.1:110 mpsname:officesvr


这样，在家里就可以通过127.0.0.1:1080端口访问公司内部网络了。

注意事项：
127.0.0.1 只能本地监听；0.0.0.0 所有地址监听，可接收外部连接。
mpsname必须对应，否则无法获取到资源。
同一个mps服务允许为多个不同的mpsname提供中转，但是不要将同名的不同资源发往一个中转服务。

如果我在外出差，我也可以利用家里的中转服务连接到公司：
7号功能：
7
127.0.0.1:1080
110.1.1.1:110
officesvr
就可以通过127.0.0.1:1080端口访问公司内部网络了。

关于资源中继的连接案例：
A网段：10.0.0.1，内部网络，只能单向访问B网络指定ip，192.168.0.10
B网段：192.168.0.10 可以单向访问外部网络
C外部网络：202.101.11.10 一个外部网络，通过ddns定位ip。
开始连接：
B、C建立mps服务端口
A机将内部端口以资源的方式发送给B的服务端口
B机使用资源中继的方式发送给C的服务端口
所有可访问到C机的终端从C机的服务端口引出A机的资源到本地

关于资源桥的连接案例：
A机：10.0.0.10  建立了mps服务端口，并且接收到了某个端口资源
B网段：192.168.0.10 可以访问A、C网络
C网络：202.101.11.10  建立了mps服务端口
开始连接：
B建立mps桥服务，设置mps资源名称，上级服务端口（A机），下级服务端口（C机）
所有可访问到C机的终端从C机的服务端口引出A机的资源到本地
包含提供给A机资源的网络和从C机取得资源的网络，这个组合可横跨5个网段，而不论转发多少端口资源，路径上的每台转发仅需不超过1个端口的资源（B机不需要端口）。

更新日志：
2013.11.05
首次完成。chentca 3746748@qq.com
2013.11.08
加入配置功能
修复bug
优化socks代理代码。
2013.11.09
充分利用go协程特点进一步优化所有握手转发环节
2016.2.23
加入telnet控制，修正部分bug
2016.3.15
加入了反向连接的标识，现在可以用一个mps服务对接多种反向连接了。
优化资源发送逻辑。
加入速度计算
加入mps默认加密，MPS资源及MPS用户与MPS服务之间简单加密。
2016.3.16
优化内存使用，所有转发纤程共用读写缓存。
2016.3.18
恢复使用线程内的缓存，不再使用公共缓存。
减小单位缓存，控制总体内存占用。
2016.3.20
增加telnet的回删BS功能。
其他小调整。
2016.3.23
增加了资源中继和资源桥两种类型，方便资源的多级传递。
2016.3.24
修改异或加密算法，原来是用数字，现在用字符，不支持汉字。
此更新版本由于加密方法变更，与旧版本不兼容。
2016.4.6
代码优化。
2016.4.8
加入tcp和udp互转的功能，但未能解决udp丢包的问题。
解决udp丢包以后再尝试做udp握手直连。
2016.4.9
分别实现了udp-tcp-udp 和 tcp-udp-tcp 的传递。
UTU由于udp在外部，无法保证也不需要考虑udp的丢包问题。
TUT中udp传输部分，使用最简单的方式，不太完美的解决了udp丢包重发的判断。
为老爸庆生，老爸生日快乐！
2016.8.26
在为网盘提供socks代理服务的时候发现原先的2分钟时限对传输造成影响，取消了连接的时长限制。
2016.9.14
尝试建立转发纤程重复使用的机制，降低内存申请频率和开销。
2016.10.11
为telnet增加密码验证
2016.10.17
调整连接超时设置，以期保持资源连接的活性。

2016.10.18
为多级连接优化握手过程（握手协议变动，与之前版本不兼容！）
2016.10.28
对复用纤程增加了存活的超时判断。
优化了网速计算的逻辑，更加准确
