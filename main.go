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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	VerStr         string = "2023-06-21.1"
	HttpClient     *FoxHTTPClient
	DefJobCount    int    = 5
	DefTimeOut     int    = 9
	DefUserAgent   string = "wofjaweirwl"
	TmpDir         string = "m3u8TMP"
	TsURList       []string
	CheckTimeStamp bool = false
)

func init() {
	HttpClient = NewFoxHTTPClient()
}

func main() {
	// 命令行参数
	flag.Usage = func() {
		fmt.Println("# Version:", VerStr)
		fmt.Println("# Usage:", os.Args[0], "[args] m3u8URL")
		flag.PrintDefaults()
		os.Exit(0)
	}
	bFFMPEG := false
	flag.BoolVar(&bFFMPEG, "z", bFFMPEG, "下载完后，调用ffmpeg转为:../xxx.mp4")
	flag.BoolVar(&CheckTimeStamp, "c", CheckTimeStamp, "慎用:分配任务前检查TS时间戳，如果到现在小于3小时，就删除并加入下载列表")
	flag.IntVar(&DefJobCount, "n", DefJobCount, "TS下载线程数[1-9]")
	flag.IntVar(&DefTimeOut, "t", DefTimeOut, "连接超时时间，单位秒")
	flag.StringVar(&TmpDir, "d", "auto", "临时文件夹，例如:/dev/shm/2233/")
	flag.StringVar(&DefUserAgent, "u", DefUserAgent, "HTTP头部User-Agent字段")
	flag.Parse()             // 处理参数
	fileCount := flag.NArg() // 处理后的参数个数，一般是URL
	m3u8URL := "https://xxxxx.com/1687250810/33000/2233/2233.m3u8"
	if fileCount == 1 {
		m3u8URL = flag.Arg(0)
	}

	// 下载并读取m3u8
	fmt.Println("# 开始 :", m3u8URL)
	m3u8Name := GetFileNameOfURL(m3u8URL)
	vid := strings.ReplaceAll(strings.ReplaceAll(m3u8Name, ".M3U8", ""), ".m3u8", "")
	if "auto" == TmpDir {
		TmpDir = vid // 临时目录
	}

	// if fileCount == 0 {
	// 	fmt.Println("DefJobCount:", DefJobCount)
	// 	fmt.Println("DefTimeOut:", DefTimeOut)
	// 	fmt.Println("TmpDir:", TmpDir)
	// 	os.Exit(0)
	// }

	chWorkingDir() // 创建并进入临时文件夹

	m3u8Content := ""
	if !FileExist(m3u8Name) {
		fmt.Println("- 下载:", m3u8URL)
		m3u8Name = HttpClient.getTS(m3u8URL, "")
	}
	fmt.Println("- 读取:", m3u8Name)
	m3u8Content = FileRead(m3u8Name)

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
	getTSURList(m3u8Content, m3u8URL)
	tsCount := len(TsURList)
	perTS := int(math.Ceil(float64(tsCount) / float64(DefJobCount)))
	fmt.Println("- TS数:", tsCount, "/", DefJobCount, "=", perTS)

	var wg sync.WaitGroup
	for i := 1; i <= DefJobCount; i++ {
		startNO := 0
		endNO := 0
		if i == DefJobCount { // 最后一组
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
				fmt.Println(thNum, "/", DefJobCount, ":", i+1, "/", urlCount, GetFileNameOfURL(tsURL))
				HttpClient.getTS(tsURL, "")
			}
		}(startNO, endNO, i)
	}
	wg.Wait()

	fmt.Println("# 完毕 :", m3u8URL)

	if bFFMPEG {
		_, erre := exec.LookPath("ffmpeg")
		if erre != nil {
			fmt.Println("- 木有找到ffmpeg: ", erre)
		} else {
			exec.Command("ffmpeg", "-i", m3u8Name, "-c", "copy", "../"+vid+".mp4").Output()
		}
	}
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
					if CheckTimeStamp { // 根据时间戳判断下载是否完整
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

func chWorkingDir() {
	err := os.MkdirAll(TmpDir, 0750)
	if err != nil && !os.IsExist(err) {
		fmt.Println("- Error @ createWorkingDir() MkdirAll()")
		return
	}
	err = os.Chdir(TmpDir)
	if err != nil {
		fmt.Println("- Error @ createWorkingDir() ChDir()")
		return
	}
}

type FoxHTTPClient struct {
	httpClient *http.Client
}

func NewFoxHTTPClient() *FoxHTTPClient {
	tOut, _ := time.ParseDuration(fmt.Sprintf("%ds", DefTimeOut))
	return &FoxHTTPClient{httpClient: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 9}, Timeout: tOut}}
}

func (fhc *FoxHTTPClient) getTS(iURL string, savePath string) string {
	req, _ := http.NewRequest("GET", iURL, nil)
	req.Header.Set("User-Agent", DefUserAgent)
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
	io.Copy(f, response.Body)
	response.Body.Close()
	f.Close()
	chFileLastModified(savePath, response.Header.Get("Last-Modified"))
	return savePath
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
