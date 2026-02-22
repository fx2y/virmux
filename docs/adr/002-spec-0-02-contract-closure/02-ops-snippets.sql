-- Latest 20 runs (lineage fields must be first-class)
select id,task,label,agent_id,status,boot_ms,resume_ms,snapshot_id,cost_est
from runs order by started_at desc limit 20;

-- Cohort-scoped resume mode histogram
select json_extract(e.payload,'$.resume_mode') as mode,count(*)
from events e join runs r on r.id=e.run_id
where e.kind='run.finished' and r.task='vm:resume' and r.label like 'qa-cert-%'
group by 1 order by 2 desc;

-- Artifact non-regular policy audit
select path,sha256,bytes from artifacts
where run_id=? and sha256 like 'meta:%' order by id;

-- Boundary event presence for a run
select sum(kind='vm.boot.started') as boot,
       sum(kind='vm.exec.injected') as exec,
       sum(kind='vm.exit.observed') as exit
from events where run_id=?;
