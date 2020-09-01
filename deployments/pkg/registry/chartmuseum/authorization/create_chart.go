/*
 * Tencent is pleased to support the open source community by making TKEStack
 * available.
 *
 * Copyright (C) 2012-2019 Tencent. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use
 * this file except in compliance with the License. You may obtain a copy of the
 * License at
 *
 * https://opensource.org/licenses/Apache-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OF ANY KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations under the License.
 */

package authorization

import (
	"fmt"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/proto/hapi/chart"
	"net/http"
	"tkestack.io/tke/api/registry"
	"tkestack.io/tke/pkg/apiserver/authentication"
	"tkestack.io/tke/pkg/registry/chartmuseum/model"
	"tkestack.io/tke/pkg/util/log"
)

// apiCreateChart serve http post request on /chart/api/{tenantID}/{chartGroup}/charts
func (a *authorization) apiCreateChart(w http.ResponseWriter, req *http.Request) {
	a.doAPICreateProvenance(w, req, "chart")
}

// apiCreateProvenance serve http post request on /chart/api/{tenantID}/{chartGroup}/prov
func (a *authorization) apiCreateProvenance(w http.ResponseWriter, req *http.Request) {
	a.doAPICreateProvenance(w, req, "prov")
}

func (a *authorization) doAPICreateProvenance(w http.ResponseWriter, req *http.Request, fieldName string) {
	chartGroup, err := a.validateAPICreateChart(w, req)
	if err != nil {
		return
	}
	sw := &statusBodyWrite{ResponseWriter: w}
	a.nextHandler.ServeHTTP(sw, req)
	if sw.status != http.StatusCreated {
		return
	}
	var savedResponse model.SavedResponse
	if err := json.Unmarshal(sw.body, &savedResponse); err != nil {
		log.Error("Failed to unmarshal response of chartmuseum", log.ByteString("body", sw.body), log.Err(err))
		return
	}
	if !savedResponse.Saved {
		log.Error("Chartmuseum server that does not meet expectations", log.ByteString("body", sw.body), log.Int("status", sw.status))
		return
	}
	file, header, err := req.FormFile(fieldName)
	if err != nil {
		log.Error("Failed to retrieve chart file from request", log.Err(err))
		return
	}
	ct, err := chartutil.LoadArchive(file)
	if err != nil {
		log.Error("Failed to load chart from request body", log.Err(err))
		return
	}
	if ct.Metadata == nil {
		log.Error("Chart metadata is nil after parsed", log.Any("chart", ct))
		return
	}
	if err := a.afterAPICreateChart(chartGroup, ct, header.Size); err != nil {
		log.Error("Failed to update registry chart resource", log.Err(err))
	}
}

func (a *authorization) validateAPICreateChart(w http.ResponseWriter, req *http.Request) (*registry.ChartGroup, error) {
	vars := mux.Vars(req)
	tenantID, ok := vars["tenantID"]
	if !ok || tenantID == "" {
		a.notFound(w)
		return nil, fmt.Errorf("not found")
	}
	chartGroupName, ok := vars["chartGroup"]
	if !ok || chartGroupName == "" {
		a.notFound(w)
		return nil, fmt.Errorf("not found")
	}
	chartGroupList, err := a.registryClient.ChartGroups().List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.tenantID=%s,spec.name=%s", tenantID, chartGroupName),
	})
	if err != nil {
		log.Error("Failed to list chart group by tenantID and name",
			log.String("tenantID", tenantID),
			log.String("name", chartGroupName),
			log.Err(err))
		a.internalError(w)
		return nil, err
	}
	if len(chartGroupList.Items) == 0 {
		// Chart group must first be created via console
		a.notFound(w)
		return nil, fmt.Errorf("not found")
	}
	chartGroup := chartGroupList.Items[0]
	if chartGroup.Status.Locked != nil && *chartGroup.Status.Locked {
		// Chart group is locked
		a.locked(w)
		return nil, fmt.Errorf("locked")
	}
	username, userTenantID := authentication.GetUsernameAndTenantID(req.Context())
	if username == "" && userTenantID == "" {
		log.Warn("Anonymous user try push chart",
			log.String("tenantID", tenantID),
			log.String("repo", chartGroupName))
		a.notAuthenticated(w, req)
		return nil, fmt.Errorf("not authenticated")
	}
	if userTenantID != tenantID {
		log.Warn("Not authorized user try push chart",
			log.String("tenantID", tenantID),
			log.String("repo", chartGroupName),
			log.String("userTenantID", userTenantID),
			log.String("username", username))
		a.forbidden(w)
		return nil, fmt.Errorf("forbidden")
	}
	return &chartGroup, nil
}

func (a *authorization) afterAPICreateChart(chartGroup *registry.ChartGroup, ct *chart.Chart, ctSize int64) error {
	chartList, err := a.registryClient.Charts(chartGroup.ObjectMeta.Name).List(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.tenantID=%s,spec.name=%s,spec.chartGroupName=%s", chartGroup.Spec.TenantID, ct.Metadata.Name, chartGroup.Spec.Name),
	})
	if err != nil {
		return err
	}

	needIncreaseChartCount := false
	if len(chartList.Items) == 0 {
		needIncreaseChartCount = true
		if _, err := a.registryClient.Charts(chartGroup.ObjectMeta.Name).Create(&registry.Chart{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: chartGroup.ObjectMeta.Name,
			},
			Spec: registry.ChartSpec{
				Name:           ct.Metadata.Name,
				TenantID:       chartGroup.Spec.TenantID,
				ChartGroupName: chartGroup.Spec.Name,
				Visibility:     chartGroup.Spec.Visibility,
			},
			Status: registry.ChartStatus{
				PullCount: 0,
				Versions: []registry.ChartVersion{
					{
						Version:     ct.Metadata.Version,
						ChartSize:   ctSize,
						TimeCreated: metav1.Now(),
					},
				},
			},
		}); err != nil {
			log.Error("Failed to create chart while pushed chart",
				log.String("tenantID", chartGroup.Spec.TenantID),
				log.String("chartGroupName", chartGroup.Spec.Name),
				log.String("chartName", ct.Metadata.Name),
				log.String("version", ct.Metadata.Version),
				log.Err(err))
			return err
		}
	} else {
		chartObject := chartList.Items[0]
		existVersion := false
		if len(chartObject.Status.Versions) == 0 {
			needIncreaseChartCount = true
		} else {
			for k, v := range chartObject.Status.Versions {
				if v.Version == ct.Metadata.Version {
					existVersion = true
					chartObject.Status.Versions[k] = registry.ChartVersion{
						Version:     ct.Metadata.Version,
						ChartSize:   ctSize,
						TimeCreated: metav1.Now(),
					}
					if _, err := a.registryClient.Charts(chartGroup.ObjectMeta.Name).UpdateStatus(&chartObject); err != nil {
						log.Error("Failed to update chart version while chart pushed",
							log.String("tenantID", chartGroup.Spec.TenantID),
							log.String("chartGroupName", chartGroup.Spec.Name),
							log.String("chartName", ct.Metadata.Name),
							log.String("version", ct.Metadata.Version),
							log.Err(err))
						return err
					}
					break
				}
			}
		}

		if !existVersion {
			chartObject.Status.Versions = append(chartObject.Status.Versions, registry.ChartVersion{
				Version:     ct.Metadata.Version,
				ChartSize:   ctSize,
				TimeCreated: metav1.Now(),
			})
			if _, err := a.registryClient.Charts(chartGroup.ObjectMeta.Name).UpdateStatus(&chartObject); err != nil {
				log.Error("Failed to create repository tag while received notification",
					log.String("tenantID", chartGroup.Spec.TenantID),
					log.String("chartGroupName", chartGroup.Spec.Name),
					log.String("chartName", ct.Metadata.Name),
					log.String("version", ct.Metadata.Version),
					log.Err(err))
				return err
			}
		}
	}

	if needIncreaseChartCount {
		// update chart group's chart count
		chartGroup.Status.ChartCount = chartGroup.Status.ChartCount + 1
		if _, err := a.registryClient.ChartGroups().UpdateStatus(chartGroup); err != nil {
			log.Error("Failed to update chart group's chart count while pushed",
				log.String("tenantID", chartGroup.Spec.TenantID),
				log.String("chartGroupName", chartGroup.Spec.Name),
				log.String("chartName", ct.Metadata.Name),
				log.String("version", ct.Metadata.Version),
				log.Err(err))
			return err
		}
	}
	return nil
}
