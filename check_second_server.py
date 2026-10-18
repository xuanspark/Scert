import paramiko

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
try:
    ssh.connect('47.108.78.12', port=22, username='root', password='10102442Lb!', timeout=10)
    print('SSH to 47.108.78.12 succeeded!')
    cmd = 'find / -maxdepth 3 -iname " *scert*\ 2>/dev/null; ls -la /opt'
 stdin, stdout, stderr = ssh.exec_command(cmd)
 print('--- Search result for scert on 47.108.78.12 ---')
 print(stdout.read().decode('utf-8', errors='ignore'))
 ssh.close()
except Exception as e:
 print('Error:', e)
