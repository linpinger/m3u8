package main

// 目标: 先能处理简单的m3u8，暂不修改m3u8文件
// 流程: 下载m3u8，读取分析，下载key，拆分任务，下载TS

import (
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"strconv"
	"sync"
	"time"
)

const VerStr string = "2025-11-17.14"

type FoxCFG struct {
	CheckTimeStamp bool
	TmpDir         string
	DefUserAgent   string
	DefJobCount    int
	DefTimeOut     int
}

var (
	CFG            *FoxCFG
	HttpClient     *FoxHTTPClient
	TsURList       []string
)

func init() {
	CFG = &FoxCFG {
		DefUserAgent: "wewwapple2xsd",
		TmpDir: "auto",
		DefJobCount: 3,
		DefTimeOut: 18,
		CheckTimeStamp:	false,
	}
	HttpClient = NewFoxHTTPClient()
}

func main() {
	m3u8URL := "https://xxxxx.com/33000/2233/2233.m3u8"

	// 命令行参数
	flag.Usage = func() {
		fmt.Println("# Version:", VerStr)
		fmt.Println("# Usage:", os.Args[0], "[args] m3u8URL")
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.BoolVar(&CFG.CheckTimeStamp, "c", CFG.CheckTimeStamp, "慎用:分配任务前检查TS时间戳，如果到现在小于3小时，就删除并加入下载列表")
	flag.IntVar(&CFG.DefJobCount, "n", CFG.DefJobCount, "TS下载线程数[1-9]")
	flag.IntVar(&CFG.DefTimeOut, "t", CFG.DefTimeOut, "连接超时时间，单位秒")
	flag.StringVar(&CFG.TmpDir, "d", CFG.TmpDir, "临时文件夹，例如:/dev/shm/2233/")
	flag.StringVar(&CFG.DefUserAgent, "u", CFG.DefUserAgent, "HTTP头部User-Agent字段")
	flag.Parse()             // 处理参数
	switch flag.NArg() {
		case 1: m3u8URL = flag.Arg(0)
		default: flag.Usage()
	}

	// start:
	fmt.Println("# 开始:", m3u8URL)
	m3u8Content := ""

	m3u8Name := GetFileNameOfURL(m3u8URL) // 2233.m3u8
	if !FileExist(m3u8Name) {
		fmt.Println("- 下载:", m3u8URL)
		m3u8Content = HttpClient.getText(m3u8URL)
	}

	// 如果m3u8中包含 #EXT-X-STREAM-INF，获取子m3u8地址并合成
	if strings.Contains(m3u8Content, "#EXT-X-STREAM-INF") {
		newM3U8URL := getSubM3U8(m3u8Content, m3u8URL)
		m3u8Name = GetFileNameOfURL(newM3U8URL) // 2233.m3u8
		if !FileExist(m3u8Name) {
			fmt.Println("- 下载:", newM3U8URL)
			m3u8Content = HttpClient.getText(newM3U8URL)
		}
		m3u8URL = newM3U8URL // 修改原始URL，后面可能用到
	}

	// 解析URL得到文件名及临时目录
	if "auto" == CFG.TmpDir {
		CFG.TmpDir = strings.ReplaceAll(strings.ReplaceAll(m3u8Name, ".M3U8", ""), ".m3u8", "") // 临时目录: 2233
	}
	chWorkingDir() // 创建并进入临时文件夹

	// 保存m3u8
	FileWrite(m3u8Content, m3u8Name)

	// 下载key
	keyURL := getKeyURL(m3u8Content, m3u8URL)
	if "" == keyURL {
		fmt.Println("- 木有key")
	} else {
		keyName := GetFileNameOfURL(keyURL)
		if !FileExist(keyName) {
			fmt.Println("- 下载:", keyURL)
			HttpClient.getTS(keyURL, "")
		}
	}

	// 获取ts url 并按任务数分配
	getTSURList(m3u8Content, m3u8URL) // 解析得到 TsURList
	tsCount := len(TsURList)
	perTS := int(math.Ceil(float64(tsCount) / float64(CFG.DefJobCount)))
	fmt.Println("- TS数:", tsCount, "/", CFG.DefJobCount, "=", perTS)

	// 分配任务
	var wg sync.WaitGroup
	for i := 1; i <= CFG.DefJobCount; i++ {
		startNO := 0
		endNO := 0
		if i == CFG.DefJobCount { // 最后一组
			startNO = perTS * (i - 1)
			endNO = tsCount
		} else {
			startNO = perTS * (i - 1)
			endNO = perTS * i
		}
		// fmt.Println(i, startNO, endNO)
		// fmt.Println(TsURList[startNO:endNO])
		wg.Add(1)
		go func(startIDX int, endIDX int, thNum int) {
			urlCount := endIDX - startIDX
			defer wg.Done()
			for i, tsURL := range TsURList[startIDX:endIDX] {
				fmt.Println(thNum, "/", CFG.DefJobCount, ":", i+1, "/", urlCount, GetFileNameOfURL(tsURL))
				HttpClient.getTS(tsURL, "")
			}
		}(startNO, endNO, i)
	}
	wg.Wait()

	fmt.Println("# 完毕 :", m3u8URL)

}

func getTSURList(iM3U8 string, iM3U8URL string) {
	lines := strings.Split(iM3U8, "\n")
	for _, line := range lines {
		if !strings.Contains(line, "#EXT") {
			if len(strings.ReplaceAll(line, " ", "")) > 1 {
				// tsURL := strings.ReplaceAll(line, "\r", "")
				tsURL := GetFullURL(strings.ReplaceAll(line, "\r", ""), iM3U8URL)
				tsName := GetFileNameOfURL(tsURL)
				fi, err := os.Stat(tsName)
				if err == nil || os.IsExist(err) {
					if CFG.CheckTimeStamp { // 根据时间戳判断下载是否完整
						if fi.Size() < 1024 { // 小于1K: 2023-12-03 add
							os.Remove(tsName)
						}
						if time.Since(fi.ModTime()).Hours() < 3 { // 小于3小时判断时间
							TsURList = append(TsURList, tsURL)
							os.Remove(tsName)
						}
					}
				} else {
					TsURList = append(TsURList, tsURL)
				}
			}
		}
	}
}

func getKeyURL(iM3U8 string, iM3U8URL string) string { // 根据m3u8内容，得到key的url
	// #EXT-X-KEY:METHOD=AES-128,URI="9aedba13f70820ff.ts",IV=0x631ed265ac39b469859dfcbeb362c75f
	if !strings.Contains(iM3U8, "#EXT-X-KEY") {
		return ""
	}
	uu := regexp.MustCompile("(?smi)EXT-X-KEY.*?URI=\"([^\"]+)\"").FindStringSubmatch(iM3U8)
	if 2 != len(uu) {
		fmt.Println("- Error @ getKeyURL() : unkown keyString")
		return ""
	}
	return GetFullURL(uu[1], iM3U8URL)
}

// 可能有bug，获取#EXT-X-STREAM-INF的下一行，如果不是m3u8就会异常 ^_^
func getSubM3U8(iM3U8 string, iM3U8URL string) string {
	lines := strings.Split(iM3U8, "\n")
	nCount := len(lines)
	sURL := ""
	for n, line := range lines {
		if strings.Contains(line, "#EXT-X-STREAM-INF") {
			if n+1 <= nCount && len(lines[n+1]) > 2 {
				sURL = lines[n+1]
				break
			} else if n+2 <= nCount && len(lines[n+2]) > 2 {
				sURL = lines[n+2]
				break
			}
		}
	}

	if "" == sURL {
		return ""
	} else {
		return GetFullURL(sURL, iM3U8URL)
	}
}

func FileExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

func FileRead(iPath string) string {
	bytes, err := os.ReadFile(iPath)
	if err != nil {
		fmt.Println("- Error @ FileRead() :", err)
		return ""
	}
	return string(bytes)
}

func FileWrite(content, oPath string) {
	err := os.WriteFile(oPath, []byte(content), os.ModePerm)
	if err != nil {
		fmt.Println("- Error @ FileWrite() :", err)
	}
}

func chWorkingDir() {
	err := os.MkdirAll(CFG.TmpDir, 0750)
	if err != nil && !os.IsExist(err) {
		fmt.Println("- Error @ createWorkingDir() MkdirAll()")
		return
	}
	err = os.Chdir(CFG.TmpDir)
	if err != nil {
		fmt.Println("- Error @ createWorkingDir() ChDir()")
		return
	}
}

type FoxHTTPClient struct {
	httpClient *http.Client
}

func NewFoxHTTPClient() *FoxHTTPClient {
	tOut, _ := time.ParseDuration(fmt.Sprintf("%ds", CFG.DefTimeOut))
	return &FoxHTTPClient{httpClient: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 9}, Timeout: tOut}}
}

func (fhc *FoxHTTPClient) getTS(iURL string, savePath string) string {
	req, _ := http.NewRequest("GET", iURL, nil)
	req.Header.Set("User-Agent", CFG.DefUserAgent)
	req.Header.Set("Connection", "keep-alive")

	response, err := fhc.httpClient.Do(req)
	if nil != err {
		fmt.Println("- Error @ getTS() :", err)
		return ""
	}
	defer response.Body.Close()

	if "" == savePath {
		savePath = GetFileNameOfURL(iURL)
	}
	f, _ := os.OpenFile(savePath, os.O_RDWR|os.O_CREATE, 0666)
	defer f.Close()
	writeLen, err := io.Copy(f, response.Body)
	if err != nil {
		fmt.Println("- Error @ getTS() io.Copy():", err)
		return ""
	}
	response.Body.Close()
	f.Close()
	hLen := response.Header.Get("Content-Length")
	if "" != hLen {
		if hLen == strconv.FormatInt(writeLen, 10) {
			chFileLastModified(savePath, response.Header.Get("Last-Modified"))
		} else {
			fmt.Println("- Error @ getTS() 文件未下载完毕 :", savePath)
			return ""
		}
	} else {
		chFileLastModified(savePath, response.Header.Get("Last-Modified"))
	}
	return savePath
}

func (fhc *FoxHTTPClient) getText(iURL string) string {
	req, _ := http.NewRequest("GET", iURL, nil)
	req.Header.Set("User-Agent", CFG.DefUserAgent)
	req.Header.Set("Connection", "keep-alive")

	response, err := fhc.httpClient.Do(req)
	if nil != err {
		fmt.Println("- Error @ getText() :", err)
		return ""
	}
	defer response.Body.Close()

	bys, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Println("- Error @ getText() io.ReadAll():", err)
		return ""
	}
	return string(bys)
}

func GetFileNameOfURL(iURL string) string {
	uu, _ := url.Parse(iURL)
	return filepath.Base(uu.Path)
}

func GetFullURL(subURL, baseURL string) string {
	bu, _ := url.Parse(baseURL)
	pu, _ := bu.Parse(subURL)
	return pu.String()
}

func chFileLastModified(filePath string, fileLastModified string) { // "Last-Modified"
	if "" != fileLastModified {
		myLoc, _ := time.LoadLocation("Asia/Shanghai")
		mtime, _ := time.ParseInLocation(time.RFC1123, fileLastModified, myLoc)
		atime, _ := time.Parse(time.RFC1123, fileLastModified)
		if err := os.Chtimes(filePath, atime, mtime); err != nil {
			fmt.Println("- Error @ chFileLastModified() :", err)
		}
	}
}
