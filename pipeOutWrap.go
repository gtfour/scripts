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

var cmdIsEmpty      = errors.New("cmd is empty")
var countTooShort   = errors.New("count to short")
var parseError      = errors.New("parse error")
var cantOpenNewFile = errors.New("can't open new file")

type Runner struct {

    cmd         *exec.Cmd
    stdout      io.ReadCloser
    ch          chan string
    quitCapture chan bool
    quitHandle  chan bool
    quit        chan bool
    count       int
    compress    bool

}

func main() {

    cmd_line,count,compress,err := parseInput()
    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner,err := NewRunner(cmd_line,count,compress)
    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner.run()
}

func parseInput()(cmd []string, count int, compress bool ,  err error){

    var cmdLine string

    cmdLinePtr  := flag.String("cmd","","Command to run")
    countPtr    := flag.Int("count",0,"Lines count")
    compressPtr := flag.Bool("compress",false,"Compress")

    flag.Parse()

    if cmdLinePtr  != nil {  cmdLine   = *cmdLinePtr  } else { err = parseError ; return }
    if countPtr    != nil {  count     = *countPtr    } else { err = parseError ; return }
    if compressPtr != nil {  compress  = *compressPtr } else { err = parseError ; return }

    if cmdLine == "" { err = cmdIsEmpty  ; return }
    if count   < 1 { err = countTooShort ; return }

    cmd   = strings.Split(cmdLine," ")

    return

}

func NewRunner( cmd_line []string ,count int,compress bool )( *Runner , error){

    var r Runner
    cmd,err       := Command(cmd_line)
    if err != nil { return nil,err }
    r.cmd         =  cmd
    r.ch          = make(chan string,100)
    r.quitCapture = make(chan bool)
    r.quitHandle  = make(chan bool)
    r.quit        = make(chan bool)
    r.count       = count
    r.compress    = compress
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
    go func() {
        for _ = range signalChan {
            //fmt.Printf("\nCleaning buffer ... please wait\n")
            r.quitCapture <- true
            <-r.quit
            cleanupDone <- true
        }
    }()
    <-cleanupDone

}



func(r *Runner)capture()(){
    //
    lineReader := bufio.NewReader(r.stdout)
    for {
        select {
            default:
                line,isPrefix,err := lineReader.ReadLine()
                _ = isPrefix
                //fmt.Printf("line: %v\nisPrefix: %v\nerr: %v\n",string(line),isPrefix,err)
                if err== nil {
                    r.ch<-string(line)
                } else {
                    break
                }
            case <- r.quitCapture:
                r.cmd.Process.Kill()
                r.quitHandle<-true
        }
    }
    // close(ch)
    //
}


func (r *Runner)handle()(){
    //
    finish := false
    var f *os.File
    var err error
    var cmdName string
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
                        if f!=nil { f.Sync() ;  f.Close() ;  }
                        counter     =  0
                        t           := time.Now()
                        timestamp   := t.Format("20060102150405")
                        cmdName     =  "logfile."+timestamp
                        if len(r.cmd.Args) > 0 {
                            cmdName = r.cmd.Args[0] + "." + cmdName
                        }
                        f, err = os.Create("./" + cmdName)
                        if err != nil { return }
                        blank = false
                    }
                    _,err = f.WriteString(s+"\n")
                    counter += 1
                    if (counter >= r.count) || ( err!= nil )  { blank = true }
                    fmt.Println(s)
            case <-r.quitHandle:
                finish = true
            default:
                if finish {  r.quit<-true ; break }
        }
    }
    if f!=nil { f.Sync() ;  ; f.Close() }
}

func compress()(){


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

