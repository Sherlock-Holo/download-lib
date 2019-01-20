package downloadLib

/*func TestDownload(t *testing.T) {
	job, err := StartDownloadJob(
		"https://mirrors.tuna.tsinghua.edu.cn/archlinux/iso/latest/archlinux-2019.01.01-x86_64.iso",
		4,
		"/tmp",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(job.TmpDir)
	t.Log(job.GID)
	t.Log(job.Filename)
	t.Log(job.SaveDir)
	t.Log(job.TotalSize)
	t.Log(len(job.requests))

	for !job.IsDone() {
		time.Sleep(1 * time.Second)
		fmt.Println("speed:", SpeedInt(job.Speed()))
	}
	t.Log(job.Wait())
	stat, err := os.Stat(filepath.Join(job.SaveDir, fmt.Sprintf("%s-%s", job.Filename, job.GID)))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(stat)
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
