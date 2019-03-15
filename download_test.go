package downloadLib

/*import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownload(t *testing.T) {
	job, err := StartDownloadJob(
		// "https://xiazai.xiazaiba.com/Soft/P/PPTV_5.0.2_XiaZaiBa.exe",
		"http://w3.153.yhlg.com/uploadfile/2018/notepad7550.zip",
		4,
		"/tmp",
	)
	if err != nil {
		t.Fatalf("%+v", err)
	}

	if job.Pause() != "paused" {
		t.Fatalf("not paused")
	}
	// t.Log("paused")
	log.Println("paused")

	job, err = RecoverJob(filepath.Join(job.SavePath, job.GIDFilename()+".bbdb"))
	if err != nil {
		if job != nil {
			t.Logf("job error: %+v", job.Err())
		}
		t.Fatalf("recover failed: %+v", err)
	}
	// t.Log("recovered")
	log.Println("recovered")

	for !job.IsDone() && !job.IsCancel() && !job.IsFailed() {
		time.Sleep(1 * time.Second)
		fmt.Println("speed:", SpeedInt(job.Speed()))
	}

	t.Log(job.Filename)
	t.Log(job.SavePath)
	t.Log(job.GIDFilename())
	t.Log(job.TotalSize)
	t.Log(len(job.requests))

	t.Log(job.Wait())

	time.Sleep(2 * time.Second)

	stat, err := os.Stat(filepath.Join(job.SavePath, job.GIDFilename()))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", stat)
}

type SpeedInt int64

func (si SpeedInt) String() string {
	switch {
	case si < 1024:
		return fmt.Sprintf("%d B/s", si)

	case si < 1024*1024:
		return fmt.Sprintf("%d KB/s", si/1024)

	case si < 1024*1024*1024:
		return fmt.Sprintf("%.2f MB/s", float64(si)/1024/1024)

	default:
		return fmt.Sprintf("%.2f GB/s", float64(si)/1024/1024/1024)
	}
}*/
