terraform {
  required_version = ">= 0.12, < 0.14"
}

variable "do_token" {
  description = "Digitalocean API token"
}
variable "do_domain" {
  description = "Your public domain"
}
variable "do_subdomain" {
  description = "Your public subdomain"
  default = "faasd"
}
variable "letsencrypt_email" {
  description = "Email used to order a certificate from Letsencrypt"
}
variable "do_create_record" {
  default     = false
  description = "Whether to create a DNS record on Digitalocean"
}
variable "do_region" {
  default     = "fra1"
  description = "The Digitalocean region where the faasd droplet will be created."
}
variable "ssh_key_file" {
  default     = "~/.ssh/id_rsa.pub"
  description = "Path to the SSH public key file"
}

provider "digitalocean" {
  token = var.do_token
}

data "local_file" "ssh_key"{
  filename = pathexpand(var.ssh_key_file)
}

resource "random_password" "password" {
  length = 16
  special = true
  override_special = "_-#"
}

data "template_file" "cloud_init" {
  template = "${file("cloud-config.tpl")}"
    vars = {
      gw_password=random_password.password.result,
      ssh_key=data.local_file.ssh_key.content,
      faasd_domain_name="${var.do_subdomain}.${var.do_domain}"
      letsencrypt_email=var.letsencrypt_email
    }
}

resource "digitalocean_droplet" "faasd" {
  region = var.do_region
  image  = "ubuntu-18-04-x64"
  name   = "faasd"
  size = "s-1vcpu-1gb"
  user_data = data.template_file.cloud_init.rendered
}

resource "digitalocean_record" "faasd" {
  domain = var.do_domain
  type   = "A"
  name   = "faasd"
  value  = digitalocean_droplet.faasd.ipv4_address
  # Only creates record if do_create_record is true
  count  = var.do_create_record == true ? 1 : 0
}

output "droplet_ip" {
  value = digitalocean_droplet.faasd.ipv4_address
}

output "gateway_url" {
  value = "https://${var.do_subdomain}.${var.do_domain}/"
}

output "password" {
    value = random_password.password.result
}

output "login_cmd" {
  value = "faas-cli login -g https://${var.do_subdomain}.${var.do_domain}/ -p ${random_password.password.result}"
}
