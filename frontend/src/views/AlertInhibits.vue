<template>
  <div class="page-container">
    <div class="page-header">
      <h2><el-icon><Filter /></el-icon> 抑制规则</h2>
      <p>当存在高优先级告警（source）时，自动抑制匹配的低优先级告警（target），抑制期内不通知</p>
    </div>
    <div class="section-card">
      <div class="section-header">
        <h3>抑制规则列表</h3>
        <div class="action-bar">
          <el-button plain @click="fetchData"><el-icon><Refresh /></el-icon></el-button>
          <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon> 新建</el-button>
        </div>
      </div>
      <el-table :data="items" v-loading="loading" stripe>
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column label="源匹配(存在即抑制)" min-width="240">
          <template #default="{row}"><el-tag v-for="m in safe(row.source_matchers_json)" :key="m.name+m.op+m.value" size="small" style="margin:1px;">{{ m.name }}{{ m.op }}{{ m.value }}</el-tag></template>
        </el-table-column>
        <el-table-column label="目标匹配(被抑制)" min-width="240">
          <template #default="{row}"><el-tag v-for="m in safe(row.target_matchers_json)" :key="m.name+m.op+m.value" size="small" style="margin:1px;">{{ m.name }}{{ m.op }}{{ m.value }}</el-tag></template>
        </el-table-column>
        <el-table-column label="equal_labels" min-width="180">
          <template #default="{row}"><el-tag v-for="l in safeArr(row.equal_labels_json)" :key="l" size="small" style="margin:1px;">{{ l }}</el-tag></template>
        </el-table-column>
        <el-table-column label="启用" width="70" align="center"><template #default="{row}"><el-switch :model-value="row.enabled" size="small" @change="toggle(row)" /></template></el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{row}"><el-button size="small" text style="color:var(--cyan);" @click="openEdit(row)">编辑</el-button><el-button size="small" text style="color:var(--red);" @click="handleDelete(row)">删除</el-button></template>
        </el-table-column>
      </el-table>
      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchData"
          @size-change="fetchData"
        />
      </div>
    </div>
    <el-dialog v-model="dialog" :title="editingId?'编辑':'新建'" width="600">
      <el-form :model="form" label-width="100px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="源条件(存在时)"><el-input v-model="form.source_matchers_json" type="textarea" :rows="2" placeholder='[{"name":"severity","op":"=","value":"critical"}]' /></el-form-item>
        <el-form-item label="目标条件(被抑制)"><el-input v-model="form.target_matchers_json" type="textarea" :rows="2" placeholder='[{"name":"severity","op":"=","value":"warning"}]' /></el-form-item>
        <el-form-item label="equal_labels"><el-input v-model="form.equal_labels_json" placeholder='["cluster","instance"]' /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="handleSave">保存</el-button></template>
    </el-dialog>
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAlertInhibits, createAlertInhibit, updateAlertInhibit, deleteAlertInhibit } from '../api'
import type { AlertInhibit } from '../types/alerting'
const loading=ref(false);const items=ref<AlertInhibit[]>([])
const page=ref(1);const pageSize=ref(20);const total=ref(0)
const dialog=ref(false);const editingId=ref<number|null>(null);const saving=ref(false)
const form=ref<AlertInhibit>({name:'',source_matchers_json:'[]',target_matchers_json:'[]',equal_labels_json:'[]',enabled:true})
function safe(s:string){try{return JSON.parse(s)||[]}catch{return[]}}
function safeArr(s:string){try{return JSON.parse(s)||[]}catch{return[]}}
async function fetchData(){loading.value=true;try{const r=await getAlertInhibits({page:page.value,page_size:pageSize.value});items.value=r.data.items;total.value=r.data.total}catch(e:any){ElMessage.error(e.message)}finally{loading.value=false}}
function openCreate(){editingId.value=null;form.value={name:'',source_matchers_json:'[]',target_matchers_json:'[]',equal_labels_json:'[]',enabled:true};dialog.value=true}
function openEdit(r:AlertInhibit){editingId.value=r.id!;form.value={...r};dialog.value=true}
async function handleSave(){saving.value=true;try{if(editingId.value){await updateAlertInhibit(editingId.value,form.value)}else{await createAlertInhibit(form.value)}ElMessage.success('成功');dialog.value=false;await fetchData()}catch(e:any){ElMessage.error(e.message)}finally{saving.value=false}}
async function toggle(r:AlertInhibit){try{await updateAlertInhibit(r.id!,{...r,enabled:!r.enabled});await fetchData()}catch(e:any){ElMessage.error(e.message)}}
async function handleDelete(r:AlertInhibit){try{await ElMessageBox.confirm('确定删除？','确认',{type:'warning',confirmButtonText:'删除',cancelButtonText:'取消'});await deleteAlertInhibit(r.id!);await fetchData()}catch{}}
onMounted(fetchData)
</script>

<style scoped>
.pager { display: flex; justify-content: flex-end; padding: 12px 4px; }
</style>
