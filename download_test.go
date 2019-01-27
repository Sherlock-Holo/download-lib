package downloadLib

// func TestDownload(t *testing.T) {
// 	job, err := StartDownloadJob(
// 		// "http://10.0.10.125/download/office-ato3.2.exe",
// 		// "http://10.0.10.125:803/office/office2016.iso",
// 		"http://wdl1.cache.wps.cn/wps/download/W.P.S.5554.50.345.exe",
// 		4,
// 		"/tmp",
// 	)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	t.Log(job.GID)
// 	t.Log(job.Filename)
// 	t.Log(job.SavePath)
// 	t.Log(job.GIDFilename())
// 	t.Log(job.TotalSize)
// 	t.Log(len(job.requests))
//
// 	for !job.IsDone() && !job.IsCancel() && !job.IsFailed() {
// 		time.Sleep(1 * time.Second)
// 		fmt.Println("speed:", SpeedInt(job.Speed()))
// 	}
// 	t.Log(job.Wait())
//
// 	time.Sleep(3 * time.Second)
//
// 	stat, err := os.Stat(filepath.Join(job.SavePath, job.GIDFilename()))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	t.Log(stat)
// }
//
// type SpeedInt int64
//
// func (si SpeedInt) String() string {
// 	switch {
// 	case si < 1024:
// 		return fmt.Sprintf("%d B/s", si)
//
// 	case si < 1024*1024:
// 		return fmt.Sprintf("%d KB/s", si/1024)
//
// 	case si < 1024*1024*1024:
// 		return fmt.Sprintf("%.2f MB/s", float64(si)/1024/1024)
//
// 	default:
// 		return fmt.Sprintf("%.2f GB/s", float64(si)/1024/1024/1024)
// 	}
// }
