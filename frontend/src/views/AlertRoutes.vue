<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Share /></el-icon> 通知路由</h2>
      <p>路由树决定告警如何分组、节流、发送到哪条通知通道。根路由为兜底，子路由支持标签匹配</p>
    </div>
    <div class="section-card">
      <div class="section-header">
        <h3><el-icon :size="16" :color="getCssVar('--cyan')"><List /></el-icon> 路由列表</h3>
        <div class="action-bar">
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新建</el-button>
        </div>
      </div>
      <el-table :data="treeRows" v-loading="loading" stripe row-key="id" default-expand-all :tree-props="{children:'children'}">
        <el-table-column label="名称" min-width="180">
          <template #default="{row}"><span :style="{marginLeft:row.parent_id?'24px':'0',fontWeight:row.parent_id?400:700}">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column label="匹配条件" min-width="200"><template #default="{row}"><el-tag v-for="m in safe(row.matchers_json)" :key="m.name+m.op+m.value" size="small" style="margin:1px;">{{ m.name }}{{ m.op }}{{ m.value }}</el-tag><span v-if="!row.parent_id" style="color:var(--text-tertiary);">兜底(全匹配)</span></template></el-table-column>
        <el-table-column label="group_wait" width="100" prop="group_wait" />
        <el-table-column label="group_interval" width="110" prop="group_interval" />
        <el-table-column label="repeat_interval" width="110" prop="repeat_interval" />
        <el-table-column label="限流窗口" width="110" prop="throttle_window" />
        <el-table-column label="continue" width="80" align="center"><template #default="{row}"><el-tag v-if="row.continue" size="small">on</el-tag></template></el-table-column>
        <el-table-column label="启用" width="70" align="center"><template #default="{row}"><el-switch :model-value="row.enabled" size="small" @change="toggle(row)" /></template></el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{row}"><el-button size="small" text style="color:var(--cyan);" @click="openEdit(row)">编辑</el-button><el-button size="small" text style="color:var(--red);" @click="handleDelete(row)" :disabled="!row.parent_id">删除</el-button></template>
        </el-table-column>
      </el-table>
    </div>
    <el-dialog v-model="dialog" :title="editingId?'编辑':'新建'" width="640">
      <el-form :model="form" label-width="120px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="父路由"><el-select v-model="form.parent_id" clearable placeholder="留空=根" style="width:100%;"><el-option v-for="r in items" :key="r.id" :label="r.name" :value="r.id" :disabled="r.id===editingId" /></el-select></el-form-item>
        <el-form-item label="匹配条件"><el-input v-model="form.matchers_json" type="textarea" :rows="2" placeholder='[{"name":"severity","op":"=","value":"critical"}]' /></el-form-item>
        <el-row :gutter="8">
          <el-col :span="8"><el-form-item label="group_wait"><el-input v-model="form.group_wait" placeholder="30s" /></el-form-item></el-col>
          <el-col :span="8"><el-form-item label="group_interval"><el-input v-model="form.group_interval" placeholder="5m" /></el-form-item></el-col>
          <el-col :span="8"><el-form-item label="repeat_interval"><el-input v-model="form.repeat_interval" placeholder="4h" /></el-form-item></el-col>
          <el-col :span="8"><el-form-item label="限流窗口"><el-input v-model="form.throttle_window" placeholder="留空=repeat_interval" /></el-form-item></el-col>
        </el-row>
        <el-form-item label="group_by"><el-input v-model="form.group_by_json" placeholder='["alertname","datasource_id"]' /></el-form-item>
        <el-form-item label="通知通道"><el-select v-model="form.notify_channel_ids" multiple placeholder="选择通道" style="width:100%;"><el-option v-for="ch in channels" :key="ch.id" :label="ch.name" :value="ch.id" /></el-select></el-form-item>
        <el-row :gutter="8">
          <el-col :span="12"><el-form-item label="恢复通知"><el-switch v-model="form.send_resolved" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="最大发送次数"><el-input v-model.number="form.max_send_count" type="number" min="0" placeholder="0=不限" /></el-form-item></el-col>
        </el-row>
        <el-checkbox v-model="form.continue" label="命中后继续匹配同级" />
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="handleSave">保存</el-button></template>
    </el-dialog>
  </div>
</template>
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAlertRoutes, createAlertRoute, updateAlertRoute, deleteAlertRoute, getAllNotifications } from '../api'
import type { AlertRoute } from '../types/alerting'
import type { NotificationChannel } from '../types'
function getCssVar(n:string){return getComputedStyle(document.documentElement).getPropertyValue(n).trim()}
const loading=ref(false);const items=ref<AlertRoute[]>([]);const channels=ref<NotificationChannel[]>([])
const dialog=ref(false);const editingId=ref<number|null>(null);const saving=ref(false)
const form=ref<AlertRoute>({name:'',continue:false,enabled:true,send_resolved:false,max_send_count:0})
function safe(s:string){try{return JSON.parse(s)||[]}catch{return[]}}
const treeRows=computed(()=>{
  const map=new Map<number,AlertRoute&{children:any[]}>()
  const roots:Array<AlertRoute&{children:any[]}>=[]
  for(const r of items.value){map.set(r.id!,{...r,children:[]})}
  for(const r of items.value){
    const node=map.get(r.id!)
    if(!node)continue
    if(r.parent_id&&map.has(r.parent_id)){map.get(r.parent_id)!.children.push(node)}
    else{roots.push(node)}
  }
  return roots
})
async function fetchData(){loading.value=true;try{const[r,c]=await Promise.all([getAlertRoutes(),getAllNotifications()]);items.value=r.data.items;channels.value=c.data||[]}catch(e:any){ElMessage.error(e.message)}finally{loading.value=false}}
function openCreate(){editingId.value=null;form.value={name:'',continue:false,enabled:true,send_resolved:false,max_send_count:0};dialog.value=true}
function openEdit(r:AlertRoute){editingId.value=r.id!;form.value={...r,...{notify_channel_ids:[...(r.notify_channel_ids||[])]}};dialog.value=true}
async function handleSave(){saving.value=true;try{if(editingId.value){await updateAlertRoute(editingId.value,form.value)}else{await createAlertRoute(form.value)}ElMessage.success('成功');dialog.value=false;await fetchData()}catch(e:any){ElMessage.error(e.message)}finally{saving.value=false}}
async function toggle(r:AlertRoute){try{await updateAlertRoute(r.id!,{...r,enabled:!r.enabled});await fetchData()}catch(e:any){ElMessage.error(e.message)}}
async function handleDelete(r:AlertRoute){try{await ElMessageBox.confirm(`确定删除路由「${r.name}」？`,'确认',{type:'warning',confirmButtonText:'删除',cancelButtonText:'取消'});await deleteAlertRoute(r.id!);await fetchData()}catch{}}
onMounted(fetchData)
</script>
