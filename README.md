# m3u8 下载工具

- **缘起:** 嗯，下载某不可描述网站的m3u8，x86平台用wget就行，挺稳，就是速度有点慢，手机上用sh脚本也行，就是写得蛋疼，不如用go写个

- **适用范围:** 简单的m3u8，ts路径不带参数的

- **流程:** 下载m3u8，读取分析，下载key，拆分任务，下载TS

- **编译:** 参见 (http://linpinger.olsoul.com/usr/2017-06-12_golang.html)  下的一般编译方法
