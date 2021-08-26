# Bootstrap faasd with TLS support on Digitalocean

1) [Sign up to DigitalOcean](https://www.digitalocean.com/?refcode=2962aa9e56a1&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=CopyPaste)
2) [Download Terraform](https://www.terraform.io)
3) Download the [cloud-config.tpl](cloud-config.tpl), [main.tf](main.tf) and [main.tfvars](main.tfvars) files.
   Or directly from the terminal using `curl`:
   ```bash
   $ curl -SLso main.tf https://raw.githubusercontent.com/openfaas/faasd/master/docs/bootstrap/digitalocean-terraform/main.tf
   $ curl -SLso main.tfvars https://raw.githubusercontent.com/openfaas/faasd/master/docs/bootstrap/digitalocean-terraform/main.tfvars
   $ curl -SLso cloud-config.tpl https://raw.githubusercontent.com/openfaas/faasd/master/docs/bootstrap/digitalocean-terraform/cloud-config.tpl
   ```
4) Run `terraform init`
5) Configure terraform variables as needed by updating the `main.tfvars` file:

   | Variable     | Description         | Default         |
   | ------------ | ------------------- | --------------- |
   | `do_token` | Digitalocean API token | None |
   | `do_domain` | Public domain used for the faasd gateway | None |
   | `do_subdomain` | Public subdomain used for the faasd gateway | `faasd` |
   | `letsencrypt_email` | Email used by when ordering TLS certificate from Letsencrypt | `""` |
   | `do_create_record` | When set to `true`, a new DNS record will be created. This works only if your domain (`do_domain`) is managed by Digitalocean | `false` |
   | `do_region` | Digitalocean region for creating the droplet | `fra1` |
   | `ssh_key_file` | Path to public SSH key file |`~/.ssh/id_rsa.pub` |

> Environment variables can also be used to set terraform variables when running the `terraform apply` command using the format `TF_VAR_name`.

6) Run `terraform apply`
   1) Add `-var-file=main.tfvars` if you have set the variables in `main.tfvars`.
   2) OR [use environment variables](https://www.terraform.io/docs/commands/environment-variables.html#tf_var_name) for setting the terraform variables when running the `apply` command

7) View the output for the login command and gateway URL i.e.

```
droplet_ip = 178.128.39.201
gateway_url = https://faasd.example.com/
```

8) View the output for sensitive data via `terraform output` command

```bash
terraform output login_cmd
login_cmd = faas-cli login -g http://178.128.39.201:8080/ -p rvIU49CEcFcHmqxj

terraform output password
password = rvIU49CEcFcHmqxj
```


9) Use your browser to access the OpenFaaS interface

Note that the user-data may take a couple of minutes to come up since it will be pulling in various components and preparing the machine. 
Also take into consideration the DNS propagation time for the new DNS record.

A single host with 1GB of RAM will be deployed for you, to remove at a later date simply use `terraform destroy`.
