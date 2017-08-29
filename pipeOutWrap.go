package main

// Usage:  ./test -cmd="tcpdump -i lo" -count=20
// count - number of lines inside output file

import "fmt"
import "os"
import "os/signal"
import "os/exec"
import "time"
import "errors"
import "flag"
import "strings"
import "io"
import "bufio"
import "path/filepath"
//

var cmdIsEmpty      = errors.New("cmd is empty")
var countTooShort   = errors.New("count to short")
var parseError      = errors.New("parse error")
var cantOpenNewFile = errors.New("can't open new file")
var fileDoesntExist = errors.New("file doesn't exist")
var logDirNotExists = errors.New("log-dir doesn't exist")

type Runner struct {

    cmd                *exec.Cmd
    log_dir            string
    log_dir_threshold  int
    stdout             io.ReadCloser
    ch                 chan string
    quitCapture        chan bool
    quitHandle         chan bool
    quit               chan bool
    count              int
    compress           bool
    currentLogFile     string
    timeout_sec        time.Duration

}

func main() {

    cmd_line,logDir,count,logDirThreshold,compress,err := parseInput()
    //fmt.Printf("Flags:\n%v %v %v %v\n",cmd_line,logDir,count,compress)

    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner,err := NewRunner(cmd_line,logDir,count,logDirThreshold,compress)
    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner.run()
}

func parseInput()(cmd []string, logDir string, count int, log_dir_threshold int, compress bool,  err error){

    var cmdLine string

    cmdLinePtr         := flag.String("cmd","","Command to run")
    logDirPtr          := flag.String("log-dir","./","Path to log directory")
    countPtr           := flag.Int("count",0,"Lines count")
    logDirThresholdPtr := flag.Int("log-dir-threshold",100,"Maximum log directory size MB")
    compressPtr        := flag.Bool("compress",false,"Compress")

    flag.Parse()

    if cmdLinePtr         != nil {  cmdLine           = *cmdLinePtr         } else { err = parseError ; return }
    if logDirPtr          != nil {  logDir            = *logDirPtr          } else { err = parseError ; return }
    if countPtr           != nil {  count             = *countPtr           } else { err = parseError ; return }
    if logDirThresholdPtr != nil {  log_dir_threshold = *logDirThresholdPtr } else { err = parseError ; return }
    if compressPtr        != nil {  compress          = *compressPtr        } else { err = parseError ; return }

    if cmdLine == "" { err = cmdIsEmpty  ; return }
    if count   < 1 { err = countTooShort ; return }

    cmd   = strings.Split(cmdLine," ")

    return

}

func NewRunner( cmd_line []string, log_dir string, count int, log_dir_threshold int, compress bool )( *Runner , error){

    var r Runner
    cmd,err       := Command(cmd_line)
    if err != nil { return nil,err }
    r.cmd         =  cmd
    if !strings.HasSuffix(log_dir, "/") { log_dir=log_dir+"/" }
    r.log_dir           = log_dir
    //
    _, err = os.Stat(r.log_dir)
    if os.IsNotExist(err) { return nil, logDirNotExists }
    //
    r.ch                = make(chan string,100)
    r.quitCapture       = make(chan bool)
    r.quitHandle        = make(chan bool)
    r.quit              = make(chan bool)
    r.count             = count
    r.log_dir_threshold = log_dir_threshold
    r.compress          = compress
    r.timeout_sec       = 2
    fmt.Printf("runner:\n")
    fmt.Printf("\n\tcmd_line:%v",cmd_line)
    fmt.Printf("\n\tlog_dir:%v",r.log_dir)
    fmt.Printf("\n\tch:%v",r.ch)
    fmt.Printf("\n\tquitCapture:%v",r.quitCapture)
    fmt.Printf("\n\tquitHandle:%v",r.quitHandle)
    fmt.Printf("\n\tquit:%v",r.quit)
    fmt.Printf("\n\tcount:%v",r.count)
    fmt.Printf("\n\tlog_dir_threshold:%v",r.log_dir_threshold)
    fmt.Printf("\n\tcompress:%v",r.compress)
    fmt.Printf("\n\ttimeout_sec:%v",r.timeout_sec)
    fmt.Printf("\n")
    return &r, nil

}

func (r *Runner)run()(error){

    stdout, err := r.cmd.StdoutPipe()
    if err != nil { return err }
    r.stdout = stdout
    err = r.cmd.Start()
    if err != nil { return err }
    go r.capture()
    go r.handle()
    r.catchExit()
    return nil

}

func(r *Runner)catchExit()(){

    signalChan  := make(chan os.Signal, 1)
    cleanupDone := make(chan bool)
    signal.Notify(signalChan, os.Interrupt)
    signal.Notify(signalChan, os.Kill)
    go func() {
        for _ = range signalChan {
            r.quitCapture <- true
            <-r.quit
            cleanupDone <- true
            break
        }
    }()
    <-cleanupDone
    return

}



func(r *Runner)capture()(){
    //
    // lineReader := bufio.NewReader(r.stdout)
    exit:=false
    lineReader := bufio.NewReader(r.stdout)
    var deffered string
    for {
        select {
            default:
                if exit { break }
                line,isPrefix,err := lineReader.ReadLine()
                if isPrefix && err==nil {
                    deffered+=string(line)
                    continue
                }
                if err == nil && !isPrefix {
                    lineStr := string(line)
                    r.ch<-deffered+lineStr
                    deffered = ""
                }
                if err!= nil { break }
            case <- r.quitCapture:
                exit = true
        }
    }
    r.cmd.Process.Kill()
    r.quitHandle<-true
    //
}


func (r *Runner)handle()(){
    //
    finish := false
    var f *os.File
    var err error
    var logName string
    //
    blank              := true
    counter            := 0
    //
    for {
        select {
            case s, ok := <-r.ch:
                    if !ok {
                        break
                    }
                    if blank {
                        // prepare new filename
                        if f!=nil      { f.Sync()  ; f.Close()  ; f = nil     }
                        counter     =  0
                        t           := time.Now()
                        timestamp   := t.Format("20060102150405")
                        logName     =  "logfile."+timestamp
                        if len(r.cmd.Args) > 0 {
                            cmdName := filepath.Base(r.cmd.Args[0])
                            logName = cmdName + "." + logName
                        }
                        //fmt.Printf("\ncreate file: %v\n",r.log_dir + logName)
                        new_file := r.log_dir + logName
                        f, err = os.Create(new_file)
                        if err != nil { break }
                        blank = false
                        r.currentLogFile = new_file
                        go r.cleanUp()
                    }
                    _,err = f.WriteString(s+"\n")
                    f.Sync()
                    counter += 1
                    if (counter >= r.count) || ( err!= nil )  { blank = true }
                    //fmt.Println(s)
            case <-r.quitHandle:
                finish = true
            default:
                if finish { break }
                time.Sleep(time.Second * r.timeout_sec)
        }
    }
    if f!=nil      { f.Sync() ;  f.Close() ; f = nil }
    r.quit<-true
}

func(r *Runner)cleanUp()(err error){
    //
    dirSizeMb,err := DirSizeMb(r.log_dir)
    if err!=nil{return}
    threshold     := r.log_dir_threshold
    if dirSizeMb>threshold{
        fmt.Printf("\nThreshold is fired:\tlog-dir max size threshold: %v\tlog-dir current size: %v",threshold,dirSizeMb)
        var oldestFile string
        oldestFile,err = getOldestFile(r.log_dir)
        oldestFile     = r.log_dir+oldestFile
        if err != nil { return }
        _, err = os.Stat(oldestFile)
        if os.IsNotExist(err) { return fileDoesntExist }
        if  oldestFile == r.currentLogFile { return nil }
        fmt.Printf("\nRemoving file %v",oldestFile)
        return os.Remove(oldestFile)
        //
    }
    return nil
    //
}


func Command(args []string) (cmd *exec.Cmd,err error) {
    // overwriting existing exec.Command  function 
    var name string
    if len(args) > 0 { name = args[0] }
    cmd = &exec.Cmd{
        Path: name,
        Args: args,
    }
    if filepath.Base(name) == name {
        if lp, err := exec.LookPath(name); err != nil {
            return nil,err
        } else {
            cmd.Path = lp
        }
    }
    return cmd, nil
}

func DirSizeMb(path string) (int, error) {
    var size        int64
    err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
        if !info.IsDir() {
            size += info.Size()
        }
        return err
    })
    sizeMB := int(size) / 1024 / 1024
    return sizeMB, err
}

func getOldestFile(dir_path string) (filename string,err error) {
    first_iter := true
    var fTgtName  string
    var fTgtMtime time.Time

    err = filepath.Walk(dir_path, func(_ string, info os.FileInfo, err error) error {
        if !info.IsDir() {
            fname  := info.Name()
            fmtime := info.ModTime()
            if first_iter {
                fTgtName   = fname
                fTgtMtime  = fmtime
                first_iter = false
            }
            if fmtime.Before(fTgtMtime){
                fTgtName   = fname
                fTgtMtime  = fmtime
            }
        }
        return err
    })
    return fTgtName, err
}


func avg_file_size()(){}
func delta()(){ }
