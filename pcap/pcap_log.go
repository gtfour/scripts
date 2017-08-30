package main

//
// Usage:  /scripts/pipeOutWrap -i="lo" -filter="tcp and port 22" -count=20 -log-dir="/scripts/logs" -log-dir-threshold=40
// i - interface to listen
// filter - filter packets by this keyword
// count - number of packets inside each output file
// log-dir - path to directory with output files
// log-dir-threshold - max size of log directory in Mb (script is automatically removes old files)
//

import "fmt"
import "os"
import "os/signal"
import "os/exec"
import "time"
import "errors"
import "flag"
import "strings"
import "path/filepath"
import "github.com/google/gopacket"
import "github.com/google/gopacket/pcap"
import "github.com/google/gopacket/pcapgo"
import "github.com/google/gopacket/layers"
//
var interfaceNameEmpty = errors.New("interface name is empty")
var countTooShort      = errors.New("count to short")
var parseError         = errors.New("parse error")
var cantOpenNewFile    = errors.New("can't open new file")
var fileDoesntExist    = errors.New("file doesn't exist")
var logDirNotExists    = errors.New("log-dir doesn't exist")
var deviceNotExists    = errors.New("device doesn't exist")
var unableToOpenDevice = errors.New("unable to open device")
var unableToSetFilter  = errors.New("unable to set such filter")
//

type Runner struct {

    interfaceName      string
    log_dir            string
    log_dir_threshold  int
    quitProcessing     chan bool
    quitCapture        chan bool
    quit               chan bool
    count              int
    compress           bool
    currentLogFile     string
    timeout_sec        time.Duration
    snapshot_len       uint32
    link_type          layers.LinkType
    packets            chan gopacket.Packet
    packet_source      *gopacket.PacketSource
    handle             *pcap.Handle

}

func main() {

    interfaceName,filter,logDir,count,logDirThreshold,compress,err := parseInput()
    //fmt.Printf("Flags:\n%v %v %v %v\n",cmd_line,logDir,count,compress)

    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner,err := NewRunner(interfaceName,filter,logDir,count,logDirThreshold,compress)
    if err != nil { fmt.Printf("error:%v\n",err) ; return }

    runner.run()
}

func parseInput()(interfaceName string, filter string, logDir string, count int, log_dir_threshold int, compress bool,  err error){

    interfaceNamePtr   := flag.String("i","","Interface name")
    filterPtr          := flag.String("filter","","Capture filter")
    logDirPtr          := flag.String("log-dir","./","Path to log directory")
    countPtr           := flag.Int("count",0,"Packets count inside each file")
    logDirThresholdPtr := flag.Int("log-dir-threshold",100,"Maximum log directory size MB")
    compressPtr        := flag.Bool("compress",false,"Compress")

    flag.Parse()

    if interfaceNamePtr   != nil {  interfaceName     = *interfaceNamePtr   } else { err = parseError ; return }
    if filterPtr          != nil {  filter            = *filterPtr          } else { err = parseError ; return }
    if logDirPtr          != nil {  logDir            = *logDirPtr          } else { err = parseError ; return }
    if countPtr           != nil {  count             = *countPtr           } else { err = parseError ; return }
    if logDirThresholdPtr != nil {  log_dir_threshold = *logDirThresholdPtr } else { err = parseError ; return }
    if compressPtr        != nil {  compress          = *compressPtr        } else { err = parseError ; return }

    if interfaceName == "" { err = interfaceNameEmpty ; return }
    if count          < 1  { err = countTooShort      ; return }

    return

}

func NewRunner( interfaceName string, filter string, log_dir string, count int, log_dir_threshold int, compress bool )( *Runner , error){
    // prepare new runner
    var r   Runner
    var err error
    //
    var snapshotLen uint32  = 1024
    var promiscuous bool   = false
    var timeout     time.Duration = -1 * time.Second
    handle, err := pcap.OpenLive(interfaceName, int32(snapshotLen), promiscuous, timeout)
    if err != nil {
        return nil, unableToOpenDevice
    }
    if filter != "" {
        // unableToSetFilter
        err = handle.SetBPFFilter(filter)
        if err != nil {
            return nil,unableToSetFilter
        }
    }
    r.handle       = handle
    r.snapshot_len = snapshotLen
    //
    if !strings.HasSuffix(log_dir, "/") { log_dir=log_dir+"/" }
    r.log_dir           = log_dir
    //
    _, err = os.Stat(r.log_dir)
    if os.IsNotExist(err) { return nil, logDirNotExists }
    //
    r.interfaceName     = interfaceName
    r.quitProcessing    = make(chan bool)
    r.quit              = make(chan bool)
    r.packets           = make(chan gopacket.Packet)
    r.count             = count
    r.log_dir_threshold = log_dir_threshold
    r.compress          = compress
    r.timeout_sec       = 2
    r.link_type         = layers.LinkTypeEthernet
    //
    r.packet_source     = gopacket.NewPacketSource(handle, handle.LinkType())
    //
    fmt.Printf("runner:\n")
    fmt.Printf("\n\tinterface_name:%v",r.interfaceName)
    fmt.Printf("\n\tlog_dir:%v",r.log_dir)
    fmt.Printf("\n\tquitProcessing:%v",r.quitProcessing)
    fmt.Printf("\n\tquit:%v",r.quit)
    fmt.Printf("\n\tcount:%v",r.count)
    fmt.Printf("\n\tlog_dir_threshold:%v",r.log_dir_threshold)
    fmt.Printf("\n\tcompress:%v",r.compress)
    fmt.Printf("\n\ttimeout_sec:%v",r.timeout_sec)
    fmt.Printf("\n\tlink_type:%v",r.link_type)
    fmt.Printf("\n")
    //
    return &r, nil
}

func (r *Runner)run()(error){
    go r.processing()
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
            r.quitProcessing<-true
            <-r.quit
            cleanupDone <- true
            break
        }
    }()
    <-cleanupDone
    r.handle.Close()
    return

}


func (r *Runner)processing()(){
    //
    finish := false
    var f *os.File
    var w *pcapgo.Writer
    var err error
    var logName string
    //
    blank   := true
    counter := 0
    //
    for {
        select {
            case packet:=<-r.packet_source.Packets():
                    if packet == nil { continue }
                    if blank {
                        // prepare new filename
                        if f!=nil      { f.Sync()  ; f.Close()  ; f = nil     }
                        counter     =  0
                        t           := time.Now()
                        timestamp   := t.Format("20060102150405")
                        logName     =  timestamp+".pcap"
                        logName = r.interfaceName + "." + logName
                        //fmt.Printf("\ncreate file: %v\n",r.log_dir + logName)
                        new_file_name := r.log_dir + logName
                        f, err = os.Create(new_file_name)
                        if err != nil { break }
                        w = pcapgo.NewWriter(f)
                        err = w.WriteFileHeader(uint32(r.snapshot_len), r.link_type)
                        if err != nil { break }
                        blank = false
                        r.currentLogFile = new_file_name
                        go r.cleanUp()
                    }
                    w.WritePacket(packet.Metadata().CaptureInfo, packet.Data())
                    f.Sync()
                    counter += 1
                    if (counter >= r.count) || ( err!= nil )  { blank = true }
                    //fmt.Println(s)
            case <-r.quitProcessing:
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


func checkDeviceExist(device_name string)(exist bool){
    devices, err := pcap.FindAllDevs()
    if err != nil {
        exist = false
        return exist
    }
    for _, device := range devices {
        dname:=device.Name
        if dname == device_name {
            exist = true
            break
        }
    }
    return
}

func delta()(){
}
