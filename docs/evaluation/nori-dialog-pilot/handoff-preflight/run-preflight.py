import subprocess,json,pathlib,time,datetime
root=pathlib.Path('/tmp/lola-nori-dialog-pilot')
image=subprocess.check_output(['docker','image','inspect','lola-nori-dialog-pilot:handoff','--format','{{.Id}}'],text=True).strip()
name='lola-nori-handoff-preflight'
command='NODE_OPTIONS=--max-old-space-size=1792 PILOT_ASSERT_STYLE=1 sh /opt/pilot/check.sh'
args=['docker','run','--rm','--init','--name',name,'--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','256','--memory','2048m','--cpus','2','--tmpfs','/tmp:rw,nosuid,size=536870912','--mount','type=bind,source='+str(root/'preflight')+',target=/workspace','--workdir','/workspace/nori-app','--env','HOME=/tmp/home','--entrypoint','sh',image,'-c','mkdir -p /tmp/home && '+command]
record={'purpose':'environment preflight of the historical candidate; not a live goal receipt','base_commit':'f94d30d7c8f08178c14297d67756a014d3ff089c','historical_patch':'docs/evaluation/nori-dialog-pilot/recovery/candidate.patch','image':image,'command':command,'memory_mib':2048,'cpus':2,'pids':256,'scratch_bytes':536870912,'network':'none','root':'read-only','started_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
start=time.monotonic()
try:
 with (root/'preflight.log').open('w') as log:
  result=subprocess.run(args,stdout=log,stderr=subprocess.STDOUT,timeout=930)
 record['exit_code']=result.returncode
except subprocess.TimeoutExpired:
 record['exit_code']=124
finally:
 subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 record['elapsed_seconds']=round(time.monotonic()-start,3)
 record['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 (root/'preflight-result.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record,indent=2))
raise SystemExit(record['exit_code'])
