import paramiko, os

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect("8.137.23.95", port=22, username="root", password="10102442Lb!", timeout=10)

sftp = ssh.open_sftp()

local_dist = "frontend/dist"
remote_web = "/scert/web"

# 上传所有文件
for root, dirs, files in os.walk(local_dist):
    rel_root = os.path.relpath(root, local_dist).replace("\\", "/")
    remote_dir = remote_web if rel_root == "." else f"{remote_web}/{rel_root}"
    try:
        sftp.mkdir(remote_dir)
    except:
        pass
    for file in files:
        local_file = os.path.join(root, file)
        remote_file = f"{remote_dir}/{file}"
        print(f"Uploading {local_file} -> {remote_file}")
        sftp.put(local_file, remote_file)

sftp.close()

# 检查远程 /scert/web 文件
stdin, stdout, stderr = ssh.exec_command("ls -la /scert/web /scert/web/assets")
print(stdout.read().decode())

ssh.close()
print("Deploy frontend done!")
