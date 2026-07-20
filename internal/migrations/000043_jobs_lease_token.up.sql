-- jobs 的锁必须带随机 owner token：同一 worker_id 在进程重启后也不能代表旧执行者。
-- worker 持有任务期间会续写 locked_at；recovery 只回收已停止续租的遗留任务。
ALTER TABLE jobs
    ADD COLUMN lease_token VARCHAR(64) NULL AFTER locked_at,
    ADD KEY idx_jobs_running_locked_at (status, locked_at);
