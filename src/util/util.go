package util

import (
    "cowsay"
    "encoding/json"
    "fmt"
    "github.com/cheggaaa/pb"
    "io"
    "io/ioutil"
    "job"
    "os"
    "os/signal"
    "sssh"
    "time"
)

// Get configurations form $HOME/.seshrc
type s3hrc struct {
    User     string
    Keyfile  string
    Password string
}

func Gets3hrc() (conf map[string]string, err error) {
    conf = make(map[string]string)
    fn := os.Getenv("HOME") + "/.seshrc"
    if _, err = os.Stat(fn); os.IsNotExist(err) {
        return conf, err
    }
    if buf, err := ioutil.ReadFile(fn); err != nil {
        return conf, err
    } else {
        rc := &s3hrc{}
        err = json.Unmarshal(buf, rc)
        if err != nil {
            return conf, err
        }
        conf["user"] = rc.User
        conf["keyfile"] = rc.Keyfile
        conf["password"] = rc.Password
        return conf, err
    }
}

func GirlSay(content ...interface{}) string {
    return cowsay.Format(fmt.Sprint(content))
}

// Hook for per task state changed
func report(output io.Writer, host string, color bool) {
    if color {
        output.Write([]byte(fmt.Sprintf("\033[33m========== %s ==========\033[0m\n", host)))
    } else {

        output.Write([]byte(fmt.Sprintf("========== %s ==========\n", host)))
    }
}
func SerialRun(config map[string]interface{}, host_arr []string) error {
    user, _ := config["User"].(string)
    pwd, _ := config["Password"].(string)
    keyfile, _ := config["Keyfile"].(string)
    cmd, _ := config["Cmd"].(string)
    args, _ := config["Args"].(string)
    // Format command
    cmd = format_cmd(cmd, args)
    printer, _ := config["Output"].(io.Writer)

    mgr, _ := job.NewManager()

    //Setup progress bar if the output is not os.Stdout
    var bar *pb.ProgressBar
    if printer != os.Stdout {
        bar = pb.StartNew(len(host_arr))
    }
    for _, h := range host_arr {
        s3h := sssh.NewS3h(h, user, pwd, keyfile, cmd, printer, mgr)
        go func() {
            if _, err := mgr.Receive(-1); err == nil {
                report(s3h.Output, s3h.Host, os.Stdout == printer)
                mgr.Send(s3h.Host, map[string]interface{}{"FROM": "MASTER", "BODY": "CONTINUE"})
            } else {
                mgr.Send(s3h.Host, map[string]interface{}{"FROM": "MASTER", "BODY": "STOP"})
            }
        }()
        if printer != os.Stdout {
            bar.Increment()
        }
        s3h.Work()
    }
    if printer != os.Stdout {
        bar.FinishPrint("")
    }
    return nil
}
func ParallelRun(config map[string]interface{}, host_arr []string, tmpdir string) error {
    user, _ := config["User"].(string)
    pwd, _ := config["Password"].(string)
    keyfile, _ := config["Keyfile"].(string)
    cmd, _ := config["Cmd"].(string)
    args, _ := config["Args"].(string)
    cmd = format_cmd(cmd, args)
    printer, _ := config["Output"].(io.Writer)

    // Create master, the master is used to manage go routines
    mgr, _ := job.NewManager()
    // Setup tmp directory for tmp files
    dir := fmt.Sprintf("%s/.s3h.%d", tmpdir, time.Now().Nanosecond())
    if err := os.Mkdir(dir, os.ModeDir|os.ModePerm); err != nil {
        return err
    }

    // Print cowsay wait
    //fmt.Println(girlSay("  Please wait me for a moment, Baby!  "))
    // Listen interrupt and kill signal, clear tmp files before exit.
    intqueue := make(chan os.Signal, 1)
    signal.Notify(intqueue, os.Interrupt, os.Kill)
    // If got interrupt or kill signal, delete tmp directory first, then exit with 1
    go func() {
        <-intqueue
        os.RemoveAll(dir)
        os.Exit(1)
    }()
    // If the complete all the tasks normlly, stop listenning signals and remove tmp directory
    defer func() {
        signal.Stop(intqueue)
        os.RemoveAll(dir)
    }()

    // Create tmp file for every host, then executes.
    var tmpfiles []*os.File
    for _, h := range host_arr {
        file, _ := os.Create(fmt.Sprintf("%s/%s", dir, h))
        tmpfiles = append(tmpfiles, file)
        s3h := sssh.NewS3h(h, user, pwd, keyfile, cmd, file, mgr)
        go s3h.Work()
    }

    // When a host is ready and request for continue, the master would echo CONTINUE for response to allow host to run
    size := len(host_arr)
    for {
        data, _ := mgr.Receive(-1)
        info, _ := data.(map[string]interface{})
        if info["BODY"].(string) == "BEGIN" {
            report(info["TAG"].(*sssh.Sssh).Output, info["TAG"].(*sssh.Sssh).Host, printer == os.Stdout)
            mgr.Send(info["FROM"].(string), map[string]interface{}{"FROM": "MASTER", "BODY": "CONTINUE"})
        } else if info["BODY"].(string) == "END" {
            // If master gets every hosts' END message, then it stop waiting.
            size -= 1
            if size == 0 {
                break
            }
        }
    }
    // close tmp files
    for _, f := range tmpfiles {
        f.Close()
    }
    // Merge all the hosts' output to the output file
    for _, h := range host_arr {
        fn := fmt.Sprintf("%s/%s", dir, h)
        src, _ := os.Open(fn)
        io.Copy(printer, src)
        src.Close()
        os.Remove(fn)
    }
    return nil
}
func Interact(config map[string]interface{}, host string) {
    user, _ := config["User"].(string)
    pwd, _ := config["Password"].(string)
    keyfile, _ := config["Keyfile"].(string)
    cmd, _ := config["Cmd"].(string)
    printer, _ := config["Output"].(io.Writer)

    mgr, _ := job.NewManager()
    s3h := sssh.NewS3h(host, user, pwd, keyfile, cmd, printer, mgr)
    s3h.SysLogin()
}
